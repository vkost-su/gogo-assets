package peopleforce

import (
	"strconv"
	"time"
)

// MapAsset converts one raw /assets item into an Asset. The category name and
// assignment-resolution fields (AssignedEmail, …) are filled in by the
// collector after separate API calls.
func MapAsset(raw map[string]any) Asset {
	a := Asset{
		ID:           asString(raw["id"]),
		Name:         asString(raw["name"]),
		Code:         asString(raw["code"]),
		SerialNumber: asString(raw["serial_number"]),
		Description:  asString(raw["description"]),
		LocationID:   asString(raw["location_id"]),
		CategoryID:   asString(raw["asset_category_id"]),
		CreatedAt:    parseDate(asString(raw["created_at"])),
		UpdatedAt:    parseDate(asString(raw["updated_at"])),
	}

	for _, item := range asSlice(raw["asset_assignments"]) {
		m := asMap(item)
		a.Assignments = append(a.Assignments, AssetAssignment{
			ID:         asString(m["id"]),
			UserID:     asInt(m["user_id"]),
			AssetID:    asInt(m["asset_id"]),
			IssuedOn:   asString(m["issued_on"]),
			ReturnedOn: asString(m["returned_on"]),
			CreatedAt:  parseDate(asString(m["created_at"])),
			UpdatedAt:  parseDate(asString(m["updated_at"])),
		})
	}
	return a
}

// MapAssetCategory converts one raw /asset_categories item.
func MapAssetCategory(raw map[string]any) AssetCategory {
	return AssetCategory{
		ID:   asString(raw["id"]),
		Name: asString(raw["name"]),
	}
}

// MapEmployee converts one raw /employees item.
func MapEmployee(raw map[string]any) Employee {
	pos := asMap(raw["position"])
	dept := asMap(raw["department"])
	loc := asMap(raw["location"])
	return Employee{
		ID:             asInt(raw["id"]),
		FullName:       asString(raw["full_name"]),
		Email:          asString(raw["email"]),
		EmployeeNumber: asString(raw["employee_number"]),
		Active:         asBool(raw["active"]),
		Department:     asString(dept["name"]),
		Position:       asString(pos["name"]),
		Location:       asString(loc["name"]),
	}
}

// currentAssignment returns the most recent active (non-returned) assignment
// from the asset's history, or nil if none is active.
func currentAssignment(assignments []AssetAssignment) *AssetAssignment {
	var best *AssetAssignment
	for i := range assignments {
		a := &assignments[i]
		if a.ReturnedOn != "" {
			continue // already returned
		}
		if best == nil || a.IssuedOn > best.IssuedOn {
			best = a
		}
	}
	return best
}

// ── Generic helpers ──────────────────────────────────────────────────────────

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// asString converts API values to string. PeopleForce sometimes returns id
// fields as numbers despite the OpenAPI spec declaring them as strings.
func asString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		i := int64(s)
		if float64(i) == s {
			return strconv.FormatInt(i, 10)
		}
		return strconv.FormatFloat(s, 'f', -1, 64)
	case int:
		return strconv.Itoa(s)
	}
	return ""
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// asInt handles both float64 (JSON numbers) and int.
func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// parseDate parses RFC3339 or ISO date strings, returning zero on failure.
func parseDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
