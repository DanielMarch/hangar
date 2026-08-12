//go:build integration

package events_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWebhookDispatcherHasAProductionCaller guards the defect this test was
// written in response to, and the class of defect it belongs to.
//
// internal/events shipped the entire §4.9 pipeline with NOTHING outside
// _test.go ever constructing an events.Dispatcher. Every test passed. The
// outbox half even worked in production — internal/rbac's mutations really
// do write app.outbox_event — so the failure presented as "webhooks are
// configured and nothing arrives", with a growing table and no error
// anywhere. That is defect B20 one subsystem over: Phase 2's
// catalogue.Boot had no caller for the same reason.
//
// A unit test cannot catch this, because the thing that is missing is a
// call site in package main. So this asserts on the source tree: the
// dispatcher must be constructed somewhere under cmd/ that is not a test.
//
// Deliberately a source-level assertion rather than a mock: the question
// "does anything run this in production" has no runtime answer available
// from inside a test binary, and the alternative — booting `serve` and
// waiting to see whether a delivery arrives — would be slow, flaky, and
// would still pass if the caller were added to a command nobody runs.
func TestWebhookDispatcherHasAProductionCaller(t *testing.T) {
	root := repoRoot(t)

	callers := grepGoSources(t, filepath.Join(root, "cmd"), regexp.MustCompile(`events\.Dispatcher\{`))
	require.NotEmpty(t, callers,
		"nothing under cmd/ constructs an events.Dispatcher, so §4.9's outbox is write-only in "+
			"production: rbac's mutations write app.outbox_event and nothing ever fans it out. "+
			"This is how defect B20 happened to catalogue.Boot.")

	// And it must actually be TICKED, not merely built. A dispatcher that
	// is constructed and dropped is the same defect with an extra line.
	ticks := grepGoSources(t, filepath.Join(root, "cmd"), regexp.MustCompile(`\.Tick\(`))
	require.NotEmpty(t, ticks, "the dispatcher is constructed but never ticked")

	// The stock docker-compose runs exactly one hangar service, `serve`.
	// A pump wired only into `work` therefore does not run on a default
	// installation — which is the installation Gate 5 measures.
	serveWiring := grepGoSources(t, filepath.Join(root, "cmd"), regexp.MustCompile(`runWebhookDispatcher\(`))
	var inServe bool
	for _, file := range serveWiring {
		if strings.HasSuffix(file, "serve.go") {
			inServe = true
		}
	}
	require.True(t, inServe,
		"the webhook pump is not started by `serve`. docker-compose.yml runs only `serve`, so "+
			"§4.9 would be inert on the default single-box deployment (§2 'single-process default').")
}

// grepGoSources returns the non-test .go files under dir matching pattern.
func grepGoSources(t testing.TB, dir string, pattern *regexp.Regexp) []string {
	t.Helper()

	var files []string
	for _, glob := range []string{filepath.Join(dir, "*.go"), filepath.Join(dir, "*", "*.go")} {
		matched, err := filepath.Glob(glob)
		require.NoError(t, err)
		files = append(files, matched...)
	}

	var matches []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		content, err := os.ReadFile(file)
		require.NoError(t, err)
		if pattern.Match(content) {
			matches = append(matches, file)
		}
	}
	return matches
}

func repoRoot(t testing.TB) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return root
}
