package filter

// Stats records whitelist purge counts. Device rows are present only when at
// least one field on that device lost entries.
type Stats struct {
	Loaded bool

	SoftwareBefore, SoftwareAfter         int
	LocalUsersBefore, LocalUsersAfter     int
	SaaSAccountsBefore, SaaSAccountsAfter int
	GWSAppsBefore, GWSAppsAfter           int

	Devices []DeviceStats
}

// DeviceStats is one JumpCloud device where the whitelist removed something.
type DeviceStats struct {
	Index, Total     int
	Hostname, Owner  string
	SoftwareBefore   int
	SoftwareAfter    int
	LocalUsersBefore int
	LocalUsersAfter  int
}

// Purged reports whether any category lost entries.
func (s Stats) Purged() bool {
	return s.SoftwareBefore > s.SoftwareAfter ||
		s.LocalUsersBefore > s.LocalUsersAfter ||
		s.SaaSAccountsBefore > s.SaaSAccountsAfter ||
		s.GWSAppsBefore > s.GWSAppsAfter
}

func (d DeviceStats) softwarePurged() bool { return d.SoftwareBefore > d.SoftwareAfter }
func (d DeviceStats) localUsersPurged() bool {
	return d.LocalUsersBefore > d.LocalUsersAfter
}
