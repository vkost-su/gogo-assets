package jumpcloud

import (
	"sort"
	"time"

	"gogo-assets/internal/model"
)

// ToDevice converts a collected System into the canonical JCDevice.
//
// meta carries CollectedAt/RunDate from the run; SourceAPI is set here.
//
// Pointer discipline (ТЗ §11): DiskEncrypted is copied as-is — it is already a
// *bool that is nil when System Insights did not report encryption, which the
// engine reads as DATA_GAP. MDMEnrolled/Active come from the system object that
// was definitely fetched, so they convert to a non-nil pointer.
func ToDevice(s System, meta model.Meta) model.JCDevice {
	meta.SourceAPI = "jumpcloud.systems"
	return model.JCDevice{
		Meta:                 meta,
		SystemID:             s.SystemID,
		Hostname:             s.Hostname,
		Serial:               s.SerialNumber,
		DisplayName:          s.DisplayName,
		OSType:               s.OSType,
		OSFamily:             s.OSFamily,
		OSVersion:            s.OSVersion,
		OSCodename:           s.OSCodename,
		Manufacturer:         s.Manufacturer,
		HardwareModel:        s.HardwareModel,
		OwnerEmail:           s.OwnerEmail,
		DiskEncrypted:        s.DiskEncrypted, // already *bool; nil = SI absent → DATA_GAP
		MDMEnrolled:          ptrBool(s.MDMEnrolled),
		EncryptionType:       s.EncryptionType,
		MDMVendor:            s.MDMVendor,
		Active:               ptrBool(s.Active),
		LastContact:          ptrTime(s.LastContact),
		RemoteIP:             s.RemoteIP,
		AgentVersion:         s.AgentVersion,
		MACAddresses:         s.MACAddresses,
		UnexpectedLocalUsers: s.UnexpectedLocalUsers,
	}
}

// ToUser converts a collected User into the canonical JCUser.
//
// MFAConfigured/Password*/account-state come from the user object and convert
// to non-nil pointers. JCGoEligible is already a *bool and is copied as-is
// (nil = the org-level state was unknown → DATA_GAP).
func ToUser(u User, meta model.Meta) model.JCUser {
	meta.SourceAPI = "jumpcloud.users"
	return model.JCUser{
		Meta:                 meta,
		UserID:               u.UserID,
		Email:                u.Email,
		Username:             u.Username,
		FullName:             u.FullName,
		MFAEnabled:           ptrBool(u.MFAConfigured),
		PasswordNeverExpires: ptrBool(u.PasswordNeverExpires),
		JumpCloudGoEnabled:   u.JCGoEligible,
		TOTPEnabled:          ptrBool(u.TOTPEnabled),
		MFARequired:          ptrBool(u.MFARequired),
		PasswordExpiration:   ptrTime(u.PasswordExpirationDate),
		PasswordExpired:      ptrBool(u.PasswordExpired),
		Suspended:            ptrBool(u.Suspended),
		AccountLocked:        ptrBool(u.AccountLocked),
		Activated:            ptrBool(u.Activated),
	}
}

// ToPolicyEnforcement rolls up per-system policy statuses into one record per
// policy. JumpCloud's per-system status carries no policy UUID, so PolicyID is
// the policy name. Output is sorted by PolicyID for deterministic snapshots.
func ToPolicyEnforcement(systems []System, meta model.Meta) []model.JCPolicyEnforcement {
	meta.SourceAPI = "jumpcloud.systems.policies"

	type agg struct{ applied, failed, pending int }
	byPolicy := make(map[string]*agg)
	for _, s := range systems {
		for _, ps := range s.PolicyStatuses {
			a := byPolicy[ps.Name]
			if a == nil {
				a = &agg{}
				byPolicy[ps.Name] = a
			}
			switch ps.Status {
			case "success":
				a.applied++
			case "failed", "error":
				a.failed++
			case "pending":
				a.pending++
			}
		}
	}

	out := make([]model.JCPolicyEnforcement, 0, len(byPolicy))
	for name, a := range byPolicy {
		applied := a.applied
		out = append(out, model.JCPolicyEnforcement{
			Meta:         meta,
			PolicyID:     name,
			AppliedCount: &applied,
			FailedCount:  a.failed,
			PendingCount: a.pending,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PolicyID < out[j].PolicyID })
	return out
}

func ptrBool(b bool) *bool { return &b }

func ptrTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
