package sheets

import "context"

// writeFullAndDrift writes a service's full tab (all rows) and, when driftTab is
// non-empty, its "<Name> (Drift)" companion — the SAME columns and layout, only
// the rows whose identity key is in drifted. A companion with no drifting rows is
// skipped (never recreated empty), mirroring the full-tab skip-empty rule.
//
// This is the one generic drift-tab writer every finding-backed service reuses
// (JumpCloud devices, Sophos, Google Workspace); keyOf maps a row to the identity
// key the drift engine keys findings by (system_id / endpoint_id / email), and
// drifted is that key set for the service (from serviceview.DriftedIDs).
func writeFullAndDrift[T any](ctx context.Context, s *Service, fullTab, driftTab string,
	cols []Column[T], rows []T, keyOf func(T) string, drifted map[string]struct{}, opts WriteOptions) error {
	if err := writeTab(ctx, s, fullTab, cols, rows, opts); err != nil {
		return err
	}
	if driftTab == "" || len(drifted) == 0 {
		return nil
	}
	dr := driftSubset(rows, keyOf, drifted)
	if len(dr) == 0 {
		return nil // skip-empty: nothing drifting ⇒ no companion tab
	}
	return writeTab(ctx, s, driftTab, cols, dr, opts)
}

// driftSubset returns the rows whose identity key is in drifted, preserving
// order. A clean row (key absent from drifted) is dropped; a drifting row is
// kept. It is the pure filter behind the (Drift) companions.
func driftSubset[T any](rows []T, keyOf func(T) string, drifted map[string]struct{}) []T {
	out := make([]T, 0, len(rows))
	for _, r := range rows {
		if _, ok := drifted[keyOf(r)]; ok {
			out = append(out, r)
		}
	}
	return out
}
