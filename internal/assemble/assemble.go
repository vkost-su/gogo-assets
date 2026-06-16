// Package assemble converts the raw per-collector outputs into the canonical
// model.Snapshot consumed by the drift engine.
//
// It is the single seam where the collector packages (gworkspace, jumpcloud,
// sophos) meet the canonical model: every to_model converter is invoked from
// here. The drift engine itself (classify/drift/digest/baseline) never imports
// a collector — it operates purely on package model — so this is the only place
// the two worlds touch.
//
// Build is pure and deterministic: every entity slice is sorted by its identity
// key, so identical collector output always produces byte-identical snapshots
// (the snapshot store relies on this for stable diffs).
package assemble

import (
	"sort"
	"time"

	"gogo-assets/internal/gworkspace"
	"gogo-assets/internal/jumpcloud"
	"gogo-assets/internal/model"
	"gogo-assets/internal/sophos"
)

// Sources bundles the raw results from the three collectors.
//
// Any field may be empty/nil when its collector was skipped (missing
// credentials or a partial target); the corresponding canonical shard is then
// simply empty.
type Sources struct {
	GWS       map[string]*gworkspace.UserRecord // keyed by primary email
	JCSystems []jumpcloud.System
	JCUsers   map[string]jumpcloud.User // keyed by email
	Endpoints []sophos.Endpoint
}

// Build assembles a canonical Snapshot stamped with runTimestamp (the exact UTC
// instant of the run) and runDate (YYYY-MM-DD, the logical collection day).
//
// The same runTimestamp/runDate is threaded into every entity's Meta so the
// whole snapshot shares one provenance stamp; each converter then fills in its
// own SourceAPI.
func Build(src Sources, runTimestamp time.Time, runDate string) model.Snapshot {
	meta := model.Meta{CollectedAt: runTimestamp, RunDate: runDate}
	return model.Snapshot{
		SchemaVersion:   model.SchemaVersion,
		RunDate:         runDate,
		RunTimestamp:    runTimestamp,
		JumpCloud:       buildJumpCloud(src, meta),
		Sophos:          buildSophos(src, meta),
		GoogleWorkspace: buildGWS(src, meta),
	}
}

// buildJumpCloud converts systems → devices and the user directory → identity,
// plus the per-policy enforcement rollup. Devices and identity are sorted by
// their identity key (SystemID / Email); PolicyEnforcement is already sorted by
// the converter.
func buildJumpCloud(src Sources, meta model.Meta) model.JumpCloudShard {
	devices := make([]model.JCDevice, 0, len(src.JCSystems))
	for _, s := range src.JCSystems {
		devices = append(devices, jumpcloud.ToDevice(s, meta))
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].SystemID < devices[j].SystemID })

	identity := make([]model.JCUser, 0, len(src.JCUsers))
	for _, u := range src.JCUsers {
		identity = append(identity, jumpcloud.ToUser(u, meta))
	}
	sort.Slice(identity, func(i, j int) bool { return identity[i].Email < identity[j].Email })

	return model.JumpCloudShard{
		Devices:           devices,
		Identity:          identity,
		PolicyEnforcement: jumpcloud.ToPolicyEnforcement(src.JCSystems, meta),
	}
}

// buildSophos converts endpoints and derives the tenant-level account-health
// rollup. Endpoints are sorted by EndpointID.
func buildSophos(src Sources, meta model.Meta) model.SophosShard {
	endpoints := make([]model.SophosEndpoint, 0, len(src.Endpoints))
	for _, e := range src.Endpoints {
		endpoints = append(endpoints, sophos.ToEndpoint(e, meta))
	}
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].EndpointID < endpoints[j].EndpointID })

	return model.SophosShard{
		Endpoints:     endpoints,
		AccountHealth: sophos.ToAccountHealth(src.Endpoints, meta),
	}
}

// buildGWS converts each user record → identity and flattens every record's
// enrolled devices into the device shard. Both are sorted by their identity key
// (Email / DeviceID).
func buildGWS(src Sources, meta model.Meta) model.GWSShard {
	identity := make([]model.GWSUser, 0, len(src.GWS))
	devices := make([]model.GWSDevice, 0)
	for _, rec := range src.GWS {
		identity = append(identity, gworkspace.ToUser(rec, meta))
		for _, d := range rec.Devices {
			devices = append(devices, gworkspace.ToDevice(d, meta))
		}
	}
	sort.Slice(identity, func(i, j int) bool { return identity[i].Email < identity[j].Email })
	sort.Slice(devices, func(i, j int) bool { return devices[i].DeviceID < devices[j].DeviceID })

	return model.GWSShard{Identity: identity, Devices: devices}
}
