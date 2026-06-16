package model

import "time"

// FindingKind enumerates the exactly-six finding categories the drift engine
// emits. Any new analysis must reuse one of these — the digest schema and the
// Claude-facing contract depend on the set being closed.
type FindingKind string

// The six finding kinds (ТЗ §7).
const (
	// KindBaselineDrift: a monitored field's value diverged from the baseline.
	KindBaselineDrift FindingKind = "BASELINE_DRIFT"
	// KindDataGap: a monitored field was not collected (nil) — data quality, not drift.
	KindDataGap FindingKind = "DATA_GAP"
	// KindNewEntity: present now, absent from the baseline census.
	KindNewEntity FindingKind = "NEW_ENTITY"
	// KindEntityDisappeared: in the baseline census, absent from this snapshot.
	KindEntityDisappeared FindingKind = "ENTITY_DISAPPEARED"
	// KindUnclassified: no baseline class matched the entity.
	KindUnclassified FindingKind = "UNCLASSIFIED"
	// KindClassConflict: more than one class matched and the tie was resolved.
	KindClassConflict FindingKind = "CLASS_CONFLICT"
)

// Default severities for the kinds whose severity is not derived from a field.
// These mirror digest_schema_example.json and are the single place to retune
// the priority of census/coverage findings.
//
// BASELINE_DRIFT takes its severity from the monitored field's drift tag.
// DATA_GAP is a fixed data-quality severity, independent of the field's own
// severity, because a gap is a collection problem rather than a posture breach.
const (
	SevDataGap           = SevMed
	SevNewEntity         = SevMed
	SevEntityDisappeared = SevHigh
	SevUnclassified      = SevMed
	SevClassConflict     = SevLow
)

// Entity type discriminators used in Finding.Entity.Type.
const (
	EntityDevice = "device"
	EntityUser   = "user"
)

// Entity is the self-contained subject of a finding — enough for Claude to act
// without dereferencing the snapshot.
type Entity struct {
	Type       string `json:"type"` // EntityDevice | EntityUser
	ID         string `json:"id"`   // system_id | endpoint_id | email
	Hostname   string `json:"hostname,omitempty"`
	OwnerEmail string `json:"owner_email,omitempty"`
}

// Finding is one drift-engine result. Every field needed to understand and act
// on it is inline (entity, field, was/now, class, summary) so the digest can be
// findings-first rather than asset-first.
type Finding struct {
	Kind     FindingKind `json:"kind"`
	Severity Severity    `json:"severity"`
	Service  []Service   `json:"service"`
	Entity   Entity      `json:"entity"`

	Field string `json:"field,omitempty"`
	Was   string `json:"was,omitempty"`
	Now   string `json:"now,omitempty"`

	ClassID string `json:"class_id,omitempty"`
	Summary string `json:"summary"`

	DetectedAt  time.Time `json:"detected_at"`
	FirstSeen   time.Time `json:"first_seen"`
	EvidenceRef string    `json:"evidence_ref,omitempty"`
}
