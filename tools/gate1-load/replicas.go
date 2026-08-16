package main

// replicas.go owns the processes under test and §1.4's mid-run mode
// transition.
//
// The two rows of §1.3's adversarial table the recording proxy cannot
// produce — "a replica killed mid-run" and "a replica restarted mid-run" —
// are process control, not responses, and injection.go says so explicitly:
// they belong here and are recorded in transition-log.jsonl.
//
// ── WHY `work` AND NOT `serve` ───────────────────────────────────────────
// `work` is the process that calls ESI. It holds the gateway, the Governor
// 1 ledger and the esi_ledger_mode gauge Gate 1.8 reads; `serve` builds no
// gateway at all. A Gate 1 replica is therefore a `hangar work` process,
// and the replica COUNT is the number of them.
//
// ── WHY THE PLANNER IS NOT ONE OF THEM ───────────────────────────────────
// Something has to claim due subscriptions and enqueue the River jobs the
// workers consume, and in production that is `serve` or `schedule`. Neither
// can be used here, because both write a heartbeat into app.esi_replica —
// and CountLiveReplicas, which selects solo vs clustered mode, counts rows
// regardless of role. A planner process would therefore make the N=1 run
// report two live replicas and select CLUSTERED, which is precisely the
// mode §1.4 requires that half of the gate NOT to be in.
//
// So the runner hosts the real planner itself, in-process and without a
// heartbeat (see main.go). The planner is the load DRIVER; the replicas are
// the installation under test. That split is what makes the replica count
// exact, and an exact replica count is what §1.4 calls the difference
// between a Gate 1 result and a meaningless one.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// replica is one `hangar work` process.
type replica struct {
	index       int
	metricsAddr string
	cmd         *exec.Cmd
	logFile     *os.File
}

// transitionEvent is one line of §1.5's transition-log.jsonl.
type transitionEvent struct {
	At       time.Time `json:"at"`
	Event    string    `json:"event"`
	Replica  int       `json:"replica"`
	PID      int       `json:"pid,omitempty"`
	Expected string    `json:"expected"`
	Note     string    `json:"note,omitempty"`
}

// fleet is the set of replicas under test.
type fleet struct {
	binary      string
	env         []string
	logDir      string
	basePort    int
	mu          sync.Mutex
	replicas    []*replica
	transitions []transitionEvent
	// started records when each replica process was launched, including the
	// initial fleet. See startTimes.
	started []time.Time
}

func newFleet(binary, logDir string, basePort int, env []string) *fleet {
	return &fleet{binary: binary, env: env, logDir: logDir, basePort: basePort}
}

// metricsURLs returns the scrape targets, one per replica, in index order.
func (f *fleet) metricsURLs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.replicas))
	for _, r := range f.replicas {
		out = append(out, "http://"+r.metricsAddr+"/metrics")
	}
	return out
}

// start launches n replicas and waits for each to export metrics.
func (f *fleet) start(ctx context.Context, n int) error {
	for i := 0; i < n; i++ {
		if err := f.startOne(ctx, i); err != nil {
			return err
		}
	}
	return nil
}

func (f *fleet) startOne(ctx context.Context, index int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", f.basePort+index)

	logPath := filepath.Join(f.logDir, fmt.Sprintf("replica-%d.log", index))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("gate1: opening replica log: %w", err)
	}

	cmd := exec.CommandContext(ctx, f.binary, "work")
	cmd.Env = append(append([]string{}, f.env...), "HANGAR_METRICS_ADDR="+addr)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("gate1: starting replica %d: %w", index, err)
	}

	r := &replica{index: index, metricsAddr: addr, cmd: cmd, logFile: logFile}

	f.mu.Lock()
	// Replace an existing entry at this index (a restart) rather than
	// appending, so metricsURLs stays one URL per replica.
	replaced := false
	for i, existing := range f.replicas {
		if existing.index == index {
			f.replicas[i] = r
			replaced = true
			break
		}
	}
	if !replaced {
		f.replicas = append(f.replicas, r)
	}
	f.mu.Unlock()

	f.mu.Lock()
	f.started = append(f.started, time.Now())
	f.mu.Unlock()

	if err := waitForMetrics(ctx, "http://"+addr+"/metrics", 90*time.Second); err != nil {
		return fmt.Errorf("gate1: replica %d never exported metrics (see %s): %w", index, logPath, err)
	}
	fmt.Printf("gate1: replica %d up (pid %d, metrics %s)\n", index, cmd.Process.Pid, addr)
	return nil
}

// kill stops one replica WITHOUT letting it deregister — §1.3's "a replica
// killed mid-run". A graceful shutdown would be a different and much
// easier test: the interesting case is the registration that outlives the
// process and has to age out of the liveness window on its own.
func (f *fleet) kill(index int) error {
	f.mu.Lock()
	var target *replica
	for _, r := range f.replicas {
		if r.index == index {
			target = r
			break
		}
	}
	f.mu.Unlock()
	if target == nil {
		return fmt.Errorf("gate1: no replica %d to kill", index)
	}

	pid := target.cmd.Process.Pid
	if err := target.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("gate1: killing replica %d: %w", index, err)
	}
	_ = target.cmd.Wait()
	_ = target.logFile.Close()

	f.record(transitionEvent{
		At: time.Now(), Event: "replica_killed", Replica: index, PID: pid,
		Expected: "mode holds clustered while >= 2 remain live; the dead replica's reservations expire and are reclaimed; no breach",
		Note:     "SIGKILL equivalent — the process does not deregister, so its app.esi_replica row must age out of the liveness window",
	})
	fmt.Printf("gate1: replica %d killed (pid %d)\n", index, pid)
	return nil
}

// restart brings a killed replica back — §1.3's "a replica restarted
// mid-run (N=1 -> 2 -> 1)" and §1.4's "the flush must lose no entries".
func (f *fleet) restart(ctx context.Context, index int) error {
	if err := f.startOne(ctx, index); err != nil {
		return err
	}
	f.record(transitionEvent{
		At: time.Now(), Event: "replica_restarted", Replica: index,
		PID:      f.pidOf(index),
		Expected: "esi_ledger_mode follows the registry; the flush preserves the live-cost sum; conditions 1.1-1.3 continue to hold",
	})
	return nil
}

func (f *fleet) pidOf(index int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.replicas {
		if r.index == index && r.cmd.Process != nil {
			return r.cmd.Process.Pid
		}
	}
	return 0
}

func (f *fleet) record(e transitionEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transitions = append(f.transitions, e)
}

// note records an observation against the transition log without being a
// process event — used to record what the mode gauge read either side of a
// transition, so transition-log.jsonl answers §1.4 on its own rather than
// requiring the reader to correlate it with divergence.csv by timestamp.
func (f *fleet) note(event, note string) {
	f.record(transitionEvent{At: time.Now(), Event: event, Note: note, Expected: "recorded observation"})
}

// stopAll kills every replica. Deferred by main so a run that fails part
// way through does not leave `hangar work` processes holding the database
// and the metrics ports — which has cost this project real debugging time
// more than once.
func (f *fleet) stopAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.replicas {
		if r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
			_ = r.cmd.Wait()
		}
		_ = r.logFile.Close()
	}
}

// writeTransitionLog emits §1.5's transition-log.jsonl.
func (f *fleet) writeTransitionLog(dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	var b strings.Builder
	for _, e := range f.transitions {
		line, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("gate1: encoding transition event: %w", err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	path := filepath.Join(dir, "transition-log.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("gate1: writing transition-log.jsonl: %w", err)
	}
	return nil
}

// transitionCount reports how many process transitions were recorded, so
// the summary can fail a run whose §1.4 transition never happened rather
// than reporting a pass for a condition it did not exercise.
func (f *fleet) transitionCount() (killed, restarted int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.transitions {
		switch e.Event {
		case "replica_killed":
			killed++
		case "replica_restarted":
			restarted++
		}
	}
	return killed, restarted
}

func waitForMetrics(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		resp, err := httpGet(ctx, url)
		if err == nil {
			_ = resp.Close()
			return nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return lastErr
}

func httpGet(ctx context.Context, url string) (io.Closer, error) {
	req, err := newRequest(ctx, url)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body, nil
}

// startTimes returns when each replica process began serving — the initial
// fleet start and every restart.
//
// §1.8's amended measurement excludes samples taken within a settling window
// of these. The reason is the same at both moments and is not specific to
// restarts: a replica that has not yet made an ESI request has not consulted
// the registry, so Governor1 still reports the optimistic `solo` default it
// was constructed with. At N=3 the initial start is the larger source — the
// planner hands work to one replica first, and the other two report a mode
// they have not selected until their own first job arrives.
func (f *fleet) startTimes() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]time.Time(nil), f.started...)
	for _, e := range f.transitions {
		if e.Event == "replica_restarted" {
			out = append(out, e.At)
		}
	}
	return out
}
