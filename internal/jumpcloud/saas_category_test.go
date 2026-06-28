package jumpcloud

import "testing"

func TestDeriveCategory(t *testing.T) {
	cases := []struct {
		name    string
		appName string
		domains []string
		want    string
	}{
		{"slack by name", "Slack", nil, "Communication"},
		{"github by name", "GitHub", nil, "Dev Tools"},
		{"copilot lands in AI not dev", "GitHub Copilot", nil, "AI"},
		{"okta is security", "Okta", nil, "Security/IAM"},
		{"salesforce crm", "Salesforce", nil, "CRM/Sales"},
		{"figma design", "Figma", nil, "Design"},
		{"match by domain", "Acme Portal", []string{"app.notion.so"}, "Productivity"},
		{"unknown falls through", "Totally Bespoke Tool", nil, "Other"},
		{"empty", "", nil, "Other"},
		{"case insensitive", "DROPBOX", nil, "Storage/Files"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveCategory(tc.appName, tc.domains); got != tc.want {
				t.Errorf("deriveCategory(%q, %v) = %q, want %q", tc.appName, tc.domains, got, tc.want)
			}
		})
	}
}
