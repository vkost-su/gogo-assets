package serviceview

import (
	"encoding/json"
	"testing"
	"time"

	"gogo-assets/internal/model"
)

var testTS = time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

func dev(id string) model.JCDevice { return model.JCDevice{SystemID: id, Hostname: id + "-host"} }

func deviceFinding(id string, svc model.Service) model.Finding {
	return model.Finding{
		Kind:     model.KindBaselineDrift,
		Severity: model.SevCrit,
		Service:  []model.Service{svc},
		Entity:   model.Entity{Type: model.EntityDevice, ID: id},
	}
}

// TestSplitFullAndDrift is the Phase-2 exit criterion: 2 clean + 1 drifting
// record ⇒ full has 3, drift has 1, in stable order.
func TestSplitFullAndDrift(t *testing.T) {
	records := []model.JCDevice{dev("a"), dev("b"), dev("c")} // pre-sorted by SystemID
	findings := []model.Finding{deviceFinding("b", model.ServiceJumpCloud)}

	drifted := DriftedIDs(findings, model.ServiceJumpCloud, model.EntityDevice)
	full, drift := Split(records, func(d model.JCDevice) string { return d.SystemID },
		drifted, "jumpcloud", "2026-05-05", testTS)

	if full.Count != 3 || len(full.Records) != 3 {
		t.Errorf("full: count=%d len=%d, want 3", full.Count, len(full.Records))
	}
	if drift.Count != 1 || len(drift.Records) != 1 {
		t.Fatalf("drift: count=%d len=%d, want 1", drift.Count, len(drift.Records))
	}
	if drift.Records[0].SystemID != "b" {
		t.Errorf("drift record = %q, want b", drift.Records[0].SystemID)
	}
	if full.View != ViewFull || drift.View != ViewDrift {
		t.Errorf("views = %q/%q, want full/drift", full.View, drift.View)
	}
	if full.SchemaVersion != model.SchemaVersion || drift.RunDate != "2026-05-05" {
		t.Errorf("wrapper provenance not stamped: %+v / %+v", full, drift)
	}
}

// TestSplitByteStable proves identical input yields byte-identical output.
func TestSplitByteStable(t *testing.T) {
	records := []model.JCDevice{dev("a"), dev("b"), dev("c")}
	findings := []model.Finding{deviceFinding("c", model.ServiceJumpCloud)}
	drifted := DriftedIDs(findings, model.ServiceJumpCloud, model.EntityDevice)

	marshal := func() []byte {
		_, drift := Split(records, func(d model.JCDevice) string { return d.SystemID },
			drifted, "jumpcloud", "2026-05-05", testTS)
		b, err := json.MarshalIndent(drift, "", "  ")
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}
	if string(marshal()) != string(marshal()) {
		t.Errorf("drift export is not byte-stable across identical runs")
	}
}

// TestDriftedIDsRespectsServiceAndType ensures a JumpCloud *user* finding does
// not leak into the JumpCloud *device* drift set (and vice-versa), and that a
// different service's findings are ignored.
func TestDriftedIDsRespectsServiceAndType(t *testing.T) {
	findings := []model.Finding{
		deviceFinding("dev-1", model.ServiceJumpCloud),
		{ // JC user finding — same service, different entity type
			Kind: model.KindBaselineDrift, Service: []model.Service{model.ServiceJumpCloud},
			Entity: model.Entity{Type: model.EntityUser, ID: "alice@x.com"},
		},
		deviceFinding("ep-9", model.ServiceSophos), // other service
	}

	got := DriftedIDs(findings, model.ServiceJumpCloud, model.EntityDevice)
	if _, ok := got["dev-1"]; !ok {
		t.Errorf("missing dev-1 in JC device drift set: %v", got)
	}
	if _, ok := got["alice@x.com"]; ok {
		t.Errorf("JC user leaked into JC device drift set: %v", got)
	}
	if _, ok := got["ep-9"]; ok {
		t.Errorf("Sophos endpoint leaked into JC device drift set: %v", got)
	}
	if len(got) != 1 {
		t.Errorf("drift set size = %d, want 1: %v", len(got), got)
	}
}

// TestEmptyDriftExport documents the "no drift" case: a service that was
// collected but is clean yields a full export with records and a drift export
// with count 0 — a valid, explicit "nothing to review" document. (The caller
// applies skip-empty on the full set: an absent service writes no file at all.)
func TestEmptyDriftExport(t *testing.T) {
	records := []model.JCDevice{dev("a"), dev("b")}
	drifted := DriftedIDs(nil, model.ServiceJumpCloud, model.EntityDevice)
	full, drift := Split(records, func(d model.JCDevice) string { return d.SystemID },
		drifted, "jumpcloud", "2026-05-05", testTS)

	if full.Count != 2 {
		t.Errorf("full count = %d, want 2", full.Count)
	}
	if drift.Count != 0 || len(drift.Records) != 0 {
		t.Errorf("drift count = %d len = %d, want 0", drift.Count, len(drift.Records))
	}
}

// TestSoftwareDriftAfterEarlyPurge documents that software drift is empty once
// the collection-time whitelist purge has already removed known-good apps.
func TestSoftwareDrift(t *testing.T) {
	people := []model.JCPersonSoftware{
		{OwnerEmail: "x@x.com", Apps: []model.JCSoftwareItem{{Name: "Sketchy Tool"}}, AppCount: 1},
	}
	if got := SoftwareDrift(people, nil); len(got) != 0 {
		t.Errorf("SoftwareDrift after early purge = %d people, want 0", len(got))
	}
}

func TestFilter(t *testing.T) {
	in := []string{"keep1", "drop", "keep2"}
	got := Filter(in, func(s string) bool { return s != "drop" })
	if len(got) != 2 || got[0] != "keep1" || got[1] != "keep2" {
		t.Errorf("Filter = %v, want [keep1 keep2]", got)
	}
	// original slice untouched
	if len(in) != 3 {
		t.Errorf("input mutated: %v", in)
	}
}
