// Package digest builds current/digest.json — the compact, findings-first view
// handed to Claude as an analyst (ТЗ §7, §10.5).
//
// It is not a dump of the snapshot: counts roll up at the top, each finding is
// self-contained (entity + field + was/now + class + summary + first_seen), and
// shard pointers let Claude drill into the full snapshot only when needed. The
// serialised size is held under a hard budget; on overflow the lowest-severity
// findings are dropped and Truncated is set, while the counts stay complete.
package digest

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"gogo-assets/internal/model"
)

// shardPointers maps a logical shard name to its JSON-path location in the
// snapshot, so the digest can reference data instead of inlining it.
var shardPointers = map[string]string{
	"jumpcloud_devices":            "local/current/snapshot.json#jumpcloud.devices",
	"jumpcloud_identity":           "local/current/snapshot.json#jumpcloud.identity",
	"jumpcloud_policy_enforcement": "local/current/snapshot.json#jumpcloud.policy_enforcement",
	"sophos_endpoints":             "local/current/snapshot.json#sophos.endpoints",
	"sophos_account_health":        "local/current/snapshot.json#sophos.account_health",
	"gws_identity":                 "local/current/snapshot.json#google_workspace.identity",
	"gws_devices":                  "local/current/snapshot.json#google_workspace.devices",
	"classification":               "local/current/classification.json",
}

// Digest is the Claude-facing rollup. Field order and tags mirror
// digest_schema_example.json.
type Digest struct {
	SchemaVersion   string            `json:"schema_version"`
	RunDate         string            `json:"run_date"`
	RunTimestamp    time.Time         `json:"run_timestamp_utc"`
	BaselineVersion string            `json:"baseline_version"`
	Counts          Counts            `json:"counts"`
	Findings        []model.Finding   `json:"drift_findings"`
	ShardPointers   map[string]string `json:"shard_pointers"`
	Truncated       bool              `json:"truncated"`
}

// Counts is the top-of-digest rollup. The breakdowns reflect every finding even
// when the findings list itself is truncated.
type Counts struct {
	DevicesTotal       int            `json:"devices_total"`
	UsersTotal         int            `json:"users_total"`
	Unclassified       int            `json:"unclassified"`
	FindingsBySeverity map[string]int `json:"findings_by_severity"`
	FindingsByKind     map[string]int `json:"findings_by_kind"`
}

// Build assembles the digest from the run's findings, reconciling each
// finding's FirstSeen against prev (nil on the first run), and serialises it
// under maxBytes. It returns the digest, its exact JSON bytes (what the caller
// must persist), and any marshalling error.
func Build(snap model.Snapshot, baselineVersion string, findings []model.Finding,
	prev *Digest, now time.Time, maxBytes int) (Digest, []byte, error) {

	findings = reconcileFirstSeen(findings, prev)
	sortFindings(findings)

	d := Digest{
		SchemaVersion:   model.SchemaVersion,
		RunDate:         snap.RunDate,
		RunTimestamp:    snap.RunTimestamp,
		BaselineVersion: baselineVersion,
		Counts:          counts(snap, findings),
		Findings:        findings,
		ShardPointers:   shardPointers,
	}

	buf, err := marshal(d)
	if err != nil {
		return Digest{}, nil, fmt.Errorf("marshal digest: %w", err)
	}
	if len(buf) <= maxBytes {
		return d, buf, nil
	}

	// Over budget: drop lowest-severity findings (they sort last) until it fits.
	// Estimate the drop count from the average finding size so this converges in
	// a few iterations rather than one-at-a-time.
	d.Truncated = true
	for len(buf) > maxBytes && len(d.Findings) > 0 {
		over := len(buf) - maxBytes
		avg := max(len(buf)/len(d.Findings), 1)
		drop := min(over/avg+1, len(d.Findings))
		d.Findings = d.Findings[:len(d.Findings)-drop]
		if buf, err = marshal(d); err != nil {
			return Digest{}, nil, fmt.Errorf("marshal digest: %w", err)
		}
	}
	return d, buf, nil
}

// counts builds the rollup from the snapshot totals and the full finding set.
func counts(snap model.Snapshot, findings []model.Finding) Counts {
	c := Counts{
		DevicesTotal: len(snap.JumpCloud.Devices) + len(snap.Sophos.Endpoints) + len(snap.GoogleWorkspace.Devices),
		UsersTotal:   len(snap.JumpCloud.Identity) + len(snap.GoogleWorkspace.Identity),
		FindingsBySeverity: map[string]int{
			string(model.SevCrit): 0, string(model.SevHigh): 0,
			string(model.SevMed): 0, string(model.SevLow): 0,
		},
		FindingsByKind: map[string]int{},
	}
	for _, f := range findings {
		c.FindingsBySeverity[string(f.Severity)]++
		c.FindingsByKind[string(f.Kind)]++
		if f.Kind == model.KindUnclassified {
			c.Unclassified++
		}
	}
	return c
}

// reconcileFirstSeen carries a finding's FirstSeen forward from the previous
// digest when the same (kind, entity, field) was already present, so chronic
// problems keep their original first-seen date and new ones read as new.
func reconcileFirstSeen(findings []model.Finding, prev *Digest) []model.Finding {
	if prev == nil {
		return findings
	}
	idx := make(map[string]time.Time, len(prev.Findings))
	for _, f := range prev.Findings {
		idx[findingKey(f)] = f.FirstSeen
	}
	for i := range findings {
		if seen, ok := idx[findingKey(findings[i])]; ok {
			findings[i].FirstSeen = seen
		}
	}
	return findings
}

func findingKey(f model.Finding) string {
	return string(f.Kind) + "|" + f.Entity.ID + "|" + f.Field
}

// sortFindings imposes a total order: severity desc, then kind, entity, field —
// so equal-severity findings are stable and the digest bytes are reproducible.
func sortFindings(findings []model.Finding) {
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if ra, rb := a.Severity.Rank(), b.Severity.Rank(); ra != rb {
			return ra > rb
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Entity.ID != b.Entity.ID {
			return a.Entity.ID < b.Entity.ID
		}
		return a.Field < b.Field
	})
}

func marshal(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
