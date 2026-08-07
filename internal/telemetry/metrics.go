package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// NewRegistry returns a Prometheus registry seeded with the standard process
// and Go runtime collectors. Domain-specific collectors (ESI ledger depth,
// sync run outcomes, ...) are registered by the phases that introduce them —
// Phase 0 only establishes the registry every later phase registers into.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	)
	return reg
}
