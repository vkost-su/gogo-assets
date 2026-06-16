package sophos

import (
	"sort"
	"time"
)

// MapEndpoint converts one raw /endpoint/v1/endpoints item into an Endpoint.
// Missing fields collapse to zero values — the function never fails.
//
// ownerEmail and policies are injected by the collector after separate API
// calls; they cannot be derived from the raw endpoint alone.
func MapEndpoint(
	raw map[string]any,
	alertCount int,
	detectionCount30d int,
	fetchError bool,
	ownerEmail string,
	policies []string,
) Endpoint {
	osRaw := asMap(raw["os"])
	health := asMap(raw["health"])
	threats := asMap(health["threats"])
	services := asMap(health["services"])
	person := asMap(raw["associatedPerson"])

	// Serial: top-level wins, fall back to hardware.serialNumber.
	serial := asString(raw["serialNumber"])
	if serial == "" {
		hw := asMap(raw["hardware"])
		serial = asString(hw["serialNumber"])
	}

	// Assigned products: list of {"code": "..."}.
	var products []string
	for _, p := range asSlice(raw["assignedProducts"]) {
		pm := asMap(p)
		if code := asString(pm["code"]); code != "" {
			products = append(products, code)
		}
	}

	// Policies: prefer the injected list; fall back to raw["policies"]
	// (dict keyed by policy type) for forward compatibility.
	if policies == nil {
		policiesRaw := asMap(raw["policies"])
		type kv struct {
			k string
			n string
		}
		var kvs []kv
		for k, v := range policiesRaw {
			pm := asMap(v)
			if n := asString(pm["name"]); n != "" {
				kvs = append(kvs, kv{k, n})
			}
		}
		sort.Slice(kvs, func(i, j int) bool { return kvs[i].k < kvs[j].k })
		policies = make([]string, 0, len(kvs))
		for _, x := range kvs {
			policies = append(policies, x.n)
		}
	}

	return Endpoint{
		EndpointID:        asString(raw["id"]),
		Hostname:          asString(raw["hostname"]),
		OSPlatform:        asString(osRaw["platform"]),
		OSName:            asString(osRaw["name"]),
		OSVersion:         asString(osRaw["majorVersion"]),
		SerialNumber:      serial,
		Online:            asBool(raw["online"]),
		LastSeenAt:        parseTime(asString(raw["lastSeenAt"])),
		HealthOverall:     asString(health["overall"]),
		HealthThreats:     asString(threats["status"]),
		HealthServices:    asString(services["status"]),
		TamperProtected:   asBool(raw["tamperProtectionEnabled"]),
		AssignedProducts:  products,
		OwnerLogin:        asString(person["viaLogin"]),
		OwnerName:         asString(person["name"]),
		OwnerEmail:        ownerEmail,
		Policies:          policies,
		MACAddresses:      asStringSlice(raw["macAddresses"]),
		IPv4Addresses:     asStringSlice(raw["ipv4Addresses"]),
		AlertCount:        alertCount,
		DetectionCount30d: detectionCount30d,
		FetchError:        fetchError,
	}
}

// ── Generic helpers (shared with other map_*.go) ─────────────────────────────

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

func asString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	default:
		return ""
	}
}

func asStringSlice(v any) []string {
	src := asSlice(v)
	if src == nil {
		return nil
	}
	out := make([]string, 0, len(src))
	for _, x := range src {
		if s := asString(x); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
