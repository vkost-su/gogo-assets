package sophos

import (
	"time"

	"gogo-assets/internal/model"
)

// ToEndpoint converts a collected Endpoint into the canonical SophosEndpoint.
//
// TamperProtected/Online come from the endpoint object (always fetched), so
// they convert to non-nil pointers. FetchError reflects an unavailable
// detections/alerts API and leaves the counts at their volatile zero — it does
// not affect TamperProtection, which is read from the endpoint record itself.
func ToEndpoint(e Endpoint, meta model.Meta) model.SophosEndpoint {
	meta.SourceAPI = "sophos.endpoints"
	return model.SophosEndpoint{
		Meta:              meta,
		EndpointID:        e.EndpointID,
		Hostname:          e.Hostname,
		Serial:            e.SerialNumber,
		OSPlatform:        e.OSPlatform,
		OSName:            e.OSName,
		OSVersion:         e.OSVersion,
		OwnerEmail:        e.OwnerEmail,
		OwnerLogin:        e.OwnerLogin,
		OwnerName:         e.OwnerName,
		TamperProtection:  ptrBool(e.TamperProtected),
		HealthOverall:     e.HealthOverall,
		HealthThreats:     e.HealthThreats,
		HealthServices:    e.HealthServices,
		AssignedProducts:  e.AssignedProducts,
		Policies:          e.Policies,
		Online:            ptrBool(e.Online),
		LastSeenAt:        ptrTime(e.LastSeenAt),
		AlertCount:        e.AlertCount,
		DetectionCount30d: e.DetectionCount30d,
		FetchError:        e.FetchError,
		MACAddresses:      e.MACAddresses,
		IPv4Addresses:     e.IPv4Addresses,
	}
}

// ToAccountHealth rolls the endpoint set up into a tenant-level health summary
// for the dashboard. It returns nil when there are no endpoints.
func ToAccountHealth(endpoints []Endpoint, meta model.Meta) *model.SophosAccountHealth {
	if len(endpoints) == 0 {
		return nil
	}
	meta.SourceAPI = "sophos.endpoints.rollup"
	h := &model.SophosAccountHealth{Meta: meta, EndpointsTotal: len(endpoints)}
	for _, e := range endpoints {
		switch e.HealthOverall {
		case "good":
			h.HealthGood++
		case "suspicious":
			h.HealthSuspicious++
		case "bad":
			h.HealthBad++
		default:
			h.HealthUnknown++
		}
		if !e.TamperProtected {
			h.TamperOffCount++
		}
		h.TotalAlerts += e.AlertCount
	}
	return h
}

func ptrBool(b bool) *bool { return &b }

func ptrTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
