package gworkspace

import (
	"sort"
	"strings"
	"time"

	admin "google.golang.org/api/admin/directory/v1"
	reports "google.golang.org/api/admin/reports/v1"
)

var _oauthEventMap = map[string]OAuthEventType{
	"authorize": OAuthAuthorize,
	"revoke":    OAuthRevoke,
	"activity":  OAuthActivity,
}

// MapIdentity builds Identity from a directory.users entry.
func MapIdentity(u *admin.User) Identity {
	fullName := ""
	if u.Name != nil {
		fullName = u.Name.FullName
	}
	orgUnit := u.OrgUnitPath
	if orgUnit == "" {
		orgUnit = "/"
	}
	return Identity{
		Email:         u.PrimaryEmail,
		FullName:      fullName,
		OrgUnitPath:   orgUnit,
		IsSuspended:   u.Suspended,
		IsArchived:    u.Archived,
		IsAdmin:       u.IsAdmin,
		CreatedAt:     parseRFC3339(u.CreationTime),
		RecoveryEmail: u.RecoveryEmail,
	}
}

// MapAuth builds AuthPosture from the same raw user entry.
func MapAuth(u *admin.User) AuthPosture {
	return AuthPosture{
		Is2SVEnrolled:             u.IsEnrolledIn2Sv,
		Is2SVEnforced:             u.IsEnforcedIn2Sv,
		LastLoginTime:             parseRFC3339(u.LastLoginTime),
		PasswordChangedAt:         time.Time{}, // not exposed by the SDK struct
		ChangePasswordAtNextLogin: u.ChangePasswordAtNextLogin,
	}
}

// MapLoginActivity folds a list of login events into the aggregate LoginActivity.
// `events` is the raw items slice from activities.list("login"), newest-first.
func MapLoginActivity(events []*reports.Activity, windowStart, windowEnd time.Time) LoginActivity {
	activity := LoginActivity{
		EventsWindowStart: windowStart,
		EventsWindowEnd:   windowEnd,
	}
	seen := make(map[string]struct{})
	for _, ev := range events {
		if ev.IpAddress != "" {
			if _, dup := seen[ev.IpAddress]; !dup {
				seen[ev.IpAddress] = struct{}{}
				activity.KnownIPs = append(activity.KnownIPs, ev.IpAddress)
			}
			if activity.LastLoginIP == "" {
				activity.LastLoginIP = ev.IpAddress
			}
		}
		for _, inner := range ev.Events {
			switch {
			case inner.Name == "login_success":
				activity.SuccessfulLoginCount++
			case inner.Name == "login_failure":
				activity.FailedLoginCount++
			case strings.HasPrefix(inner.Name, "suspicious_login"):
				activity.SuspiciousLoginCount++
			}
		}
	}
	sort.Strings(activity.KnownIPs)
	return activity
}

// MapOAuthGrants flattens activities.list("token") into one OAuthGrant per inner event.
func MapOAuthGrants(events []*reports.Activity) []OAuthGrant {
	var grants []OAuthGrant
	for _, ev := range events {
		var when time.Time
		if ev.Id != nil {
			when = parseRFC3339(ev.Id.Time)
		}
		if when.IsZero() {
			continue
		}
		for _, inner := range ev.Events {
			et, ok := _oauthEventMap[inner.Name]
			if !ok {
				continue
			}
			params := flattenParams(inner.Parameters)
			grants = append(grants, OAuthGrant{
				EventTime:  when,
				EventType:  et,
				AppName:    firstNonEmpty(params["app_name"], "unknown"),
				ClientID:   params["client_id"],
				Scopes:     splitScopes(params["scope"]),
				ClientType: params["client_type"],
			})
		}
	}
	return grants
}

// MapConnectedApp builds ConnectedApp from a tokens.list item.
func MapConnectedApp(t *admin.Token) ConnectedApp {
	scopes := make([]string, 0, len(t.Scopes))
	scopes = append(scopes, t.Scopes...)
	return ConnectedApp{
		ClientID:    t.ClientId,
		DisplayText: t.DisplayText,
		Scopes:      scopes,
		IsAnonymous: t.Anonymous,
		IsNativeApp: t.NativeApp,
	}
}

// MapEndpointDevice builds Device from a mobiledevices.list entry.
func MapEndpointDevice(d *admin.MobileDevice) Device {
	ownerEmail := ""
	if len(d.Email) > 0 {
		ownerEmail = d.Email[0]
	}
	deviceID := d.ResourceId
	if deviceID == "" {
		deviceID = d.DeviceId
	}
	return Device{
		DeviceID:     deviceID,
		DeviceKind:   detectDeviceKind(d),
		OwnerEmail:   ownerEmail,
		Model:        d.Model,
		Manufacturer: d.Manufacturer,
		SerialNumber: d.SerialNumber,
		OSType:       d.Os,
		OSVersion:    "", // MobileDevice has no separate version field; Os carries it
		LastSync:     parseRFC3339(d.LastSync),
		Status:       d.Status,
		MACAddresses: nil,
	}
}

func detectDeviceKind(d *admin.MobileDevice) DeviceKind {
	t := strings.ToUpper(d.Type)
	osStr := strings.ToLower(d.Os)

	switch t {
	case "ANDROID":
		return DeviceAndroid
	case "IOS":
		return DeviceIOS
	case "MAC", "MACOS":
		return DeviceMacOS
	case "WINDOWS", "WINDOWS_MOBILE":
		return DeviceWindows
	}
	switch {
	case strings.Contains(osStr, "mac"), strings.Contains(osStr, "darwin"):
		return DeviceMacOS
	case strings.Contains(osStr, "windows"):
		return DeviceWindows
	}
	return DeviceOther
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func flattenParams(params []*reports.ActivityEventsParameters) map[string]string {
	out := make(map[string]string, len(params))
	for _, p := range params {
		switch {
		case p.Value != "":
			out[p.Name] = p.Value
		case len(p.MultiValue) > 0:
			out[p.Name] = strings.Join(p.MultiValue, " ")
		}
	}
	return out
}

func splitScopes(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func parseRFC3339(s string) time.Time {
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

func firstNonEmpty(parts ...string) string {
	for _, p := range parts {
		if p != "" {
			return p
		}
	}
	return ""
}
