package gworkspace

import (
	"testing"

	"gogo-assets/internal/model"
)

func TestToUserPointerDiscipline(t *testing.T) {
	t.Run("2sv false → mfa_enabled *false", func(t *testing.T) {
		u := ToUser(&UserRecord{
			Identity: Identity{Email: "a@x.com"},
			Auth:     AuthPosture{Is2SVEnrolled: false},
		}, model.Meta{})
		if u.MFAEnabled == nil || *u.MFAEnabled != false {
			t.Errorf("MFAEnabled = %v, want non-nil *false", u.MFAEnabled)
		}
	})

	t.Run("asp/backup nil (not collected → DATA_GAP)", func(t *testing.T) {
		u := ToUser(&UserRecord{Identity: Identity{Email: "a@x.com"}}, model.Meta{})
		if u.ASPCount != nil {
			t.Errorf("ASPCount = %v, want nil (DATA_GAP)", *u.ASPCount)
		}
		if u.BackupCodeCount != nil {
			t.Errorf("BackupCodeCount = %v, want nil (DATA_GAP)", *u.BackupCodeCount)
		}
	})

	t.Run("third_party_tokens counts connected apps", func(t *testing.T) {
		u := ToUser(&UserRecord{
			Identity:      Identity{Email: "a@x.com"},
			ConnectedApps: []ConnectedApp{{ClientID: "1"}, {ClientID: "2"}},
		}, model.Meta{})
		if u.ThirdPartyTokens == nil || *u.ThirdPartyTokens != 2 {
			t.Errorf("ThirdPartyTokens = %v, want *2", u.ThirdPartyTokens)
		}
	})

	t.Run("login activity flattened", func(t *testing.T) {
		u := ToUser(&UserRecord{
			Identity:      Identity{Email: "a@x.com"},
			LoginActivity: &LoginActivity{SuccessfulLoginCount: 5, LastLoginIP: "1.2.3.4"},
		}, model.Meta{})
		if u.SuccessfulLogins != 5 || u.LastLoginIP != "1.2.3.4" {
			t.Errorf("login activity not flattened: %+v", u)
		}
	})
}
