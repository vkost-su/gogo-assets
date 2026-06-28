// Package collector defines the Collector interface that every source-data
// collector implements, allowing the orchestrator to drive them uniformly
// without importing collector-specific types.
package collector

import "context"

// Collector is the common interface for all source-data collectors.
type Collector interface {
	// Name returns the short identifier used in logs ("gws" | "jc" | "sophos").
	Name() string
	// CollectAll runs the full collection pipeline. Results are written into the
	// Output the implementation was constructed with.
	CollectAll(ctx context.Context) error
}
