package jumpcloud

import "time"

// SaaSExportSchemaVersion versions the standalone saas.json artifact so a
// downstream reader can detect shape changes independently of the canonical
// snapshot schema.
const SaaSExportSchemaVersion = "1.0"

// SaaSExport is the standalone SaaS artifact written to local/current/saas.json
// (+ a daily mirror) every run. It carries the full nested SaaSApp structures —
// exactly the data backing the SaaS sheet tab, including the owner accounts,
// license tiers, contract, and SSO connections that the flat tab summarises —
// wrapped with run provenance so the file is self-describing.
//
// Applications are in tab order (category, then name) so the file and the sheet
// read top-to-bottom the same way.
type SaaSExport struct {
	SchemaVersion string    `json:"schema_version"`
	RunDate       string    `json:"run_date"`
	RunTimestamp  time.Time `json:"run_timestamp"`
	Count         int       `json:"count"`
	Applications  []SaaSApp `json:"applications"`
}

// NewSaaSExport wraps the collected applications with run provenance. apps is
// taken as-is (already sorted by the collector); the caller stamps the run date
// and timestamp from the active run.
func NewSaaSExport(apps []SaaSApp, runDate string, runTimestamp time.Time) SaaSExport {
	return SaaSExport{
		SchemaVersion: SaaSExportSchemaVersion,
		RunDate:       runDate,
		RunTimestamp:  runTimestamp,
		Count:         len(apps),
		Applications:  apps,
	}
}
