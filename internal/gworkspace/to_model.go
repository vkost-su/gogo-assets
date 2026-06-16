package gworkspace

import (
	"time"

	"gogo-assets/internal/model"
)

// ToUser converts an enriched UserRecord into the canonical GWSUser.
//
// 2SV/admin/suspended flags come from the directory user object and convert to
// non-nil pointers. ASPCount and BackupCodeCount are deliberately left nil:
// the asps/verificationCodes endpoints are not fetched in this version, so the
// engine reports an honest DATA_GAP rather than a fabricated zero. ThirdPartyTokens
// is the count of currently-authorised OAuth apps (always known once tokens.list
// has run).
func ToUser(r *UserRecord, meta model.Meta) model.GWSUser {
	meta.SourceAPI = "gworkspace.directory"
	u := model.GWSUser{
		Meta:             meta,
		Email:            r.Identity.Email,
		FullName:         r.Identity.FullName,
		OrgUnitPath:      r.Identity.OrgUnitPath,
		MFAEnabled:       ptrBool(r.Auth.Is2SVEnrolled),
		MFAEnforced:      ptrBool(r.Auth.Is2SVEnforced),
		IsAdmin:          ptrBool(r.Identity.IsAdmin),
		ThirdPartyTokens: ptrInt(len(r.ConnectedApps)),
		Suspended:        ptrBool(r.Identity.IsSuspended),
		IsArchived:       ptrBool(r.Identity.IsArchived),
		RecoveryEmail:    r.Identity.RecoveryEmail,
		CreatedAt:        ptrTime(r.Identity.CreatedAt),
		LastLoginTime:    ptrTime(r.Auth.LastLoginTime),
	}
	if la := r.LoginActivity; la != nil {
		u.LastLoginIP = la.LastLoginIP
		u.SuccessfulLogins = la.SuccessfulLoginCount
		u.FailedLogins = la.FailedLoginCount
		u.SuspiciousLogins = la.SuspiciousLoginCount
	}
	return u
}

// ToDevice converts a directory Device into the canonical GWSDevice.
func ToDevice(d Device, meta model.Meta) model.GWSDevice {
	meta.SourceAPI = "gworkspace.mobiledevices"
	return model.GWSDevice{
		Meta:         meta,
		DeviceID:     d.DeviceID,
		DeviceKind:   string(d.DeviceKind),
		OwnerEmail:   d.OwnerEmail,
		Serial:       d.SerialNumber,
		Model:        d.Model,
		Manufacturer: d.Manufacturer,
		OSType:       d.OSType,
		OSVersion:    d.OSVersion,
		Status:       d.Status,
		LastSync:     ptrTime(d.LastSync),
		MACAddresses: d.MACAddresses,
	}
}

func ptrBool(b bool) *bool { return &b }

func ptrInt(n int) *int { return &n }

func ptrTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
