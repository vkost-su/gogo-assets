package sheets

import (
	"testing"

	"gogo-assets/internal/jumpcloud"
)

// TestSaaSSSO covers the SSO status cell: empty when there are no SSO
// connections, "Connected" when any is CONNECTED, else "Not connected".
func TestSaaSSSO(t *testing.T) {
	tests := []struct {
		name string
		give jumpcloud.SaaSApp
		want string
	}{
		{"no sso apps", jumpcloud.SaaSApp{}, ""},
		{"connected", jumpcloud.SaaSApp{SSOApps: []jumpcloud.SaaSSSOApp{{Status: "CONNECTED"}}}, "Connected"},
		{"one of many connected", jumpcloud.SaaSApp{SSOApps: []jumpcloud.SaaSSSOApp{{Status: "NOT_CONNECTED"}, {Status: "CONNECTED"}}}, "Connected"},
		{"present not connected", jumpcloud.SaaSApp{SSOApps: []jumpcloud.SaaSSSOApp{{Status: "NOT_CONNECTED"}}}, "Not connected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := saasSSO(tt.give); got != tt.want {
				t.Errorf("saasSSO() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSaaSSeats covers the "assigned / total" cell, including the unlimited and
// no-license cases.
func TestSaaSSeats(t *testing.T) {
	tests := []struct {
		name string
		give jumpcloud.SaaSApp
		want string
	}{
		{"no licenses", jumpcloud.SaaSApp{}, ""},
		{"finite tier", jumpcloud.SaaSApp{Licenses: []jumpcloud.SaaSLicense{{Count: 10, Assigned: 7, Unassigned: 3}}}, "7 / 10"},
		{"unlimited tier", jumpcloud.SaaSApp{Licenses: []jumpcloud.SaaSLicense{{Assigned: 5, IsUnlimited: true}}}, "5 / ∞"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := saasSeats(tt.give); got != tt.want {
				t.Errorf("saasSeats() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSaaSCost covers the annual cost cell: blank without a contract or a zero
// cost, integer amounts without decimals, fractional amounts with two.
func TestSaaSCost(t *testing.T) {
	tests := []struct {
		name string
		give jumpcloud.SaaSApp
		want string
	}{
		{"no contract", jumpcloud.SaaSApp{}, ""},
		{"zero cost", jumpcloud.SaaSApp{Contract: &jumpcloud.SaaSContract{Cost: 0, Currency: "USD"}}, ""},
		{"integer", jumpcloud.SaaSApp{Contract: &jumpcloud.SaaSContract{Cost: 1440, Currency: "USD"}}, "1440 USD"},
		{"fractional", jumpcloud.SaaSApp{Contract: &jumpcloud.SaaSContract{Cost: 1499.5, Currency: "EUR"}}, "1499.50 EUR"},
		{"no currency", jumpcloud.SaaSApp{Contract: &jumpcloud.SaaSContract{Cost: 99}}, "99"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := saasCost(tt.give); got != tt.want {
				t.Errorf("saasCost() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSaaSHasUnassignedSeats covers the idle-seat alert predicate over the
// rendered "assigned / total" string.
func TestSaaSHasUnassignedSeats(t *testing.T) {
	tests := []struct {
		give string
		want bool
	}{
		{"7 / 10", true},
		{"10 / 10", false},
		{"5 / ∞", false},
		{"", false},
		{"garbage", false},
	}
	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			if got := saasHasUnassignedSeats(tt.give); got != tt.want {
				t.Errorf("saasHasUnassignedSeats(%q) = %v, want %v", tt.give, got, tt.want)
			}
		})
	}
}
