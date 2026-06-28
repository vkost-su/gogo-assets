// Package service defines the uniform integration contract used by the
// inventory orchestrator.
//
// A service module owns one external integration's lifecycle: validate whether
// it is configured, build its client, collect raw data, ingest that raw data
// into the Sheets-facing inventory view, and append it to the canonical
// snapshot sources. Adding a new integration should normally mean adding one
// module implementation here (or in a package that satisfies Module) plus the
// service-specific client/collector/converter package.
package service

import (
	"context"
	"fmt"
	"log/slog"

	"gogo-assets/internal/assemble"
	"gogo-assets/internal/config"
	"gogo-assets/internal/httpstat"
	"gogo-assets/internal/inventory"
	"gogo-assets/internal/model"
)

// Key is a stable service identifier used by targets, logs, and module lookup.
type Key string

const (
	KeyGoogleWorkspace Key = "gw"
	KeyJumpCloud       Key = "jc"
	KeySophos          Key = "sp"
)

// Runtime carries shared dependencies every module needs during collection.
type Runtime struct {
	Settings    config.Settings
	HTTPCounter *httpstat.Counter
	Log         *slog.Logger
}

// Result is the uniform collection envelope returned by every module. Output is
// intentionally the service's typed raw output (*gworkspace.Output,
// *jumpcloud.Output, ...); module methods are responsible for asserting that
// type before ingesting or appending sources.
type Result struct {
	Key         Key
	Service     model.Service
	DisplayName string
	Output      any
	Queries     []string
	Counts      map[string]int
}

// Module is the contract every service integration implements.
type Module interface {
	Key() Key
	DisplayName() string
	ModelService() model.Service
	Required() bool
	Configured(config.Settings) bool
	MissingConfigMessage() string
	Collect(context.Context, Runtime) (Result, error)
	IngestInventory(*inventory.AssetInventory, Result) error
	AppendSources(*assemble.Sources, Result) error
	RawArtifactName() string
}

// Registry is an ordered set of service modules. Order is meaningful: collection
// and ingestion happen in registry order for deterministic logs and output.
type Registry []Module

// DefaultRegistry returns the production service set.
func DefaultRegistry() Registry {
	return Registry{
		GoogleWorkspaceModule{},
		JumpCloudModule{},
		SophosModule{},
	}
}

// ForTarget selects the modules requested by an inventory target.
func (r Registry) ForTarget(target string) ([]Module, error) {
	if target == "all" {
		return append([]Module(nil), r...), nil
	}
	for _, m := range r {
		if string(m.Key()) == target {
			return []Module{m}, nil
		}
	}
	return nil, fmt.Errorf("unknown target %q", target)
}

// Collect runs the selected modules, builds the unified inventory and canonical
// source bundle, and returns the per-service results for callers that need raw
// counts or manifests.
func Collect(ctx context.Context, registry Registry, rt Runtime, target string) (*inventory.AssetInventory, assemble.Sources, []Result, error) {
	modules, err := registry.ForTarget(target)
	if err != nil {
		return nil, assemble.Sources{}, nil, err
	}

	results := make([]Result, 0, len(modules))
	inv := inventory.New()
	var src assemble.Sources

	for _, m := range modules {
		if !m.Configured(rt.Settings) {
			if m.Required() {
				return nil, assemble.Sources{}, nil, fmt.Errorf("%s is not configured", m.DisplayName())
			}
			if rt.Log != nil {
				rt.Log.Info(m.MissingConfigMessage())
			}
			continue
		}

		result, err := m.Collect(ctx, rt)
		if err != nil {
			return nil, assemble.Sources{}, nil, fmt.Errorf("%s collect: %w", m.Key(), err)
		}
		if err := m.IngestInventory(inv, result); err != nil {
			return nil, assemble.Sources{}, nil, fmt.Errorf("%s inventory ingest: %w", m.Key(), err)
		}
		if err := m.AppendSources(&src, result); err != nil {
			return nil, assemble.Sources{}, nil, fmt.Errorf("%s source append: %w", m.Key(), err)
		}
		results = append(results, result)
	}

	inv.Finalize()
	return inv, src, results, nil
}

func outputAs[T any](m Module, r Result) (*T, error) {
	out, ok := r.Output.(*T)
	if !ok {
		return nil, fmt.Errorf("%s returned output %T, want *%T", m.DisplayName(), r.Output, new(T))
	}
	return out, nil
}
