package main

import (
	"log/slog"

	"gogo-assets/internal/model"
	"gogo-assets/internal/serviceview"
	"gogo-assets/internal/snapshot"
)

// writeServiceOutputs emits the per-service full/drift JSON files into the run's
// dated folder from the canonical snapshot + drift findings. It is the disk twin
// of the drift Sheets tabs: one generic serviceview.Split call per service, no
// per-service copy of the split logic.
//
// Skip-empty (mirrors the SaaS behaviour): a service with no records writes no
// files, so an absent/unlicensed service never clobbers a populated run. A
// service that was collected but is clean still writes its full file and a
// drift file with count 0 — the external report step relies on every
// *-drift.json for the run existing.
func writeServiceOutputs(log *slog.Logger, store *snapshot.Store, snap model.Snapshot, findings []model.Finding) {
	emitService(log, store, snap, findings, model.ServiceJumpCloud, model.EntityDevice,
		"jumpcloud", "jc", snap.JumpCloud.Devices,
		func(d model.JCDevice) string { return d.SystemID })

	emitService(log, store, snap, findings, model.ServiceJumpCloud, model.EntityUser,
		"jumpcloud_users", "jc-users", snap.JumpCloud.Identity,
		func(u model.JCUser) string { return u.Email })

	emitService(log, store, snap, findings, model.ServiceGoogleWorkspace, model.EntityUser,
		"google_workspace", "gw", snap.GoogleWorkspace.Identity,
		func(u model.GWSUser) string { return u.Email })

	emitService(log, store, snap, findings, model.ServiceSophos, model.EntityDevice,
		"sophos", "sp", snap.Sophos.Endpoints,
		func(e model.SophosEndpoint) string { return e.EndpointID })

	// jc-saas.json / jc-saas-drift.json — the per-person JumpCloud software
	// footprint (device apps/extensions + SaaS memberships), anchored by email.
	// Software is pre-filtered at collection time; drift is always empty.
	if len(snap.JumpCloud.Software) > 0 {
		full := serviceview.Wrap(snap.JumpCloud.Software, "jumpcloud_software",
			serviceview.ViewFull, snap.RunDate, snap.RunTimestamp)
		drift := serviceview.Wrap(serviceview.SoftwareDrift(snap.JumpCloud.Software, nil),
			"jumpcloud_software", serviceview.ViewDrift, snap.RunDate, snap.RunTimestamp)
		writeExport(log, store, snap.RunDate, "jc-saas.json", full)
		writeExport(log, store, snap.RunDate, "jc-saas-drift.json", drift)
	}
}

// emitService writes <base>.json (full) and <base>-drift.json (only entities the
// engine flagged) for one service. records must be pre-sorted by identity key
// (assemble guarantees this) so both files are byte-stable.
func emitService[T any](log *slog.Logger, store *snapshot.Store, snap model.Snapshot,
	findings []model.Finding, svc model.Service, etype, service, base string,
	records []T, keyOf func(T) string) {
	if len(records) == 0 {
		return // skip-empty: absent service ⇒ no file
	}
	drifted := serviceview.DriftedIDs(findings, svc, etype)
	full, drift := serviceview.Split(records, keyOf, drifted, service, snap.RunDate, snap.RunTimestamp)
	writeExport(log, store, snap.RunDate, base+".json", full)
	writeExport(log, store, snap.RunDate, base+"-drift.json", drift)
}

func writeExport(log *slog.Logger, store *snapshot.Store, runDate, name string, export any) {
	res, err := store.WriteDailyJSON(runDate, name, export)
	if err != nil {
		log.Error("service output write failed", "file", name, "err", err)
		return
	}
	log.Info("service output", "file", name, "size", snapshot.HumanBytes(res.SizeBytes))
}
