//go:build !race

package ratelimit

// raceDetectorEnabled is compiled true only under `go test -race` (see
// race_on.go). TestBenchmarkLedgerSolo1MOperationsMeetsBudget uses it to
// skip its wall-clock budget assertion under the race detector, whose
// instrumentation overhead (routinely 5-10x) has nothing to do with
// whether the ledger's own hot path meets §5.5's budget.
const raceDetectorEnabled = false
