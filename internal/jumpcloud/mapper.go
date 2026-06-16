package jumpcloud

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// _systemUserRE matches usernames that begin with underscore — macOS/Linux
// system service accounts that we treat as expected.
var _systemUserRE = regexp.MustCompile(`^_`)

// _knownExpectedUsers is the set of explicit usernames that are NOT flagged
// as unexpected when found on a managed endpoint.
var _knownExpectedUsers = map[string]struct{}{
	"root": {}, "nobody": {}, "daemon": {}, "admin": {}, "Guest": {},
	"adm-user": {}, "default": {},
	"_sophos": {}, // explicit catch (also matched by the regex)
}

func isExpectedUser(u string) bool {
	if _systemUserRE.MatchString(u) {
		return true
	}
	_, ok := _knownExpectedUsers[u]
	return ok
}

// ── Encryption ───────────────────────────────────────────────────────────────

// detectEncryption returns (encrypted, type, filevault) inspected across all
// disk_encryption / bitlocker_info SI rows. macOS APFS spreads partitions —
// we keep scanning instead of short-circuiting on the first unencrypted row.
func detectEncryption(diskEnc, bitlocker []map[string]any) (*bool, string, string) {
	for _, row := range diskEnc {
		val := row["encrypted"]
		encType := asString(row["type"])
		fv := asString(row["filevault_status"])
		if intLike(val, 1) || boolLike(val, true) || stringLike(val, "1") {
			t := true
			return &t, encType, fv
		}
		if fv != "" && containsCI(fv, "on", "1", "true") {
			t := true
			return &t, encType, fv
		}
	}
	if len(diskEnc) > 0 {
		// Had SI data but no encrypted partition found.
		f := false
		return &f, "", ""
	}

	for _, row := range bitlocker {
		raw := row["protection_status"]
		method := asString(row["encryption_method"])
		if method == "" {
			method = "BitLocker"
		}
		switch v := raw.(type) {
		case float64: // JSON numbers decode to float64
			if int(v) == 1 {
				t := true
				return &t, method, ""
			}
			if int(v) == 0 {
				f := false
				return &f, method, ""
			}
		case int:
			if v == 1 {
				t := true
				return &t, method, ""
			}
			if v == 0 {
				f := false
				return &f, method, ""
			}
		case string:
			low := strings.ToLower(v)
			if strings.Contains(low, "on") {
				t := true
				return &t, method, ""
			}
			if strings.Contains(low, "off") {
				f := false
				return &f, method, ""
			}
		}
	}
	return nil, "", ""
}

// ── MAC ──────────────────────────────────────────────────────────────────────

func extractMACAddresses(ifaces []map[string]any) []string {
	seen := make(map[string]struct{}, len(ifaces))
	var macs []string
	for _, iface := range ifaces {
		mac := strings.ToUpper(strings.TrimSpace(asString(iface["mac"])))
		if len(mac) < 12 {
			continue
		}
		plain := strings.ReplaceAll(strings.ReplaceAll(mac, ":", ""), "-", "")
		if plain == "000000000000" {
			continue
		}
		if _, ok := seen[mac]; ok {
			continue
		}
		seen[mac] = struct{}{}
		macs = append(macs, mac)
	}
	return macs
}

// ── Disk size ────────────────────────────────────────────────────────────────

func diskSizeBytes(osFamily string, mounts, diskInfo, blockDevs []map[string]any) *int64 {
	family := strings.ToLower(osFamily)
	switch family {
	case "darwin":
		var best int64
		for _, row := range mounts {
			bs := asInt64(row["blocks_size"])
			bl := asInt64(row["blocks"])
			if s := bs * bl; s > best {
				best = s
			}
		}
		if best == 0 {
			return nil
		}
		return &best
	case "windows":
		var best int64
		for _, row := range diskInfo {
			if s := asInt64(row["disk_size"]); s > best {
				best = s
			}
		}
		if best == 0 {
			return nil
		}
		return &best
	case "linux":
		var best int64
		for _, row := range blockDevs {
			if s := asInt64(row["size"]); s > best {
				best = s
			}
		}
		if best == 0 {
			return nil
		}
		return &best
	}
	return nil
}

// ── Software mapping ─────────────────────────────────────────────────────────

func mapApps(rows []map[string]any) []App {
	out := make([]App, 0, len(rows))
	for _, row := range rows {
		name := asString(row["name"])
		if name == "" {
			name = asString(row["bundle_name"])
		}
		if name == "" {
			continue
		}
		version := asString(row["bundle_short_version"])
		if version == "" {
			version = asString(row["bundle_version"])
		}
		out = append(out, App{
			Name:       name,
			Version:    version,
			LastOpened: epochToDateString(row["last_opened_time"]),
		})
	}
	sortAppsByName(out)
	return out
}

func mapPrograms(rows []map[string]any) []App {
	out := make([]App, 0, len(rows))
	for _, row := range rows {
		name := asString(row["name"])
		if name == "" {
			continue
		}
		installDate := asString(row["install_date"])
		// Windows returns YYYYMMDD; format it to YYYY-MM-DD for display.
		if len(installDate) == 8 {
			installDate = installDate[:4] + "-" + installDate[4:6] + "-" + installDate[6:]
		}
		out = append(out, App{
			Name:       name,
			Version:    asString(row["version"]),
			LastOpened: installDate,
		})
	}
	sortAppsByName(out)
	return out
}

func mapLinuxPackages(rows []map[string]any) []App {
	out := make([]App, 0, len(rows))
	for _, row := range rows {
		name := asString(row["name"])
		if name == "" {
			continue
		}
		out = append(out, App{Name: name, Version: asString(row["version"])})
	}
	sortAppsByName(out)
	return out
}

func mapExtensions(rows []map[string]any) []App {
	out := make([]App, 0, len(rows))
	for _, row := range rows {
		name := asString(row["name"])
		if name == "" {
			name = asString(row["identifier"])
		}
		if name == "" {
			continue
		}
		out = append(out, App{Name: name, Version: asString(row["version"])})
	}
	sortAppsByName(out)
	return out
}

func sortAppsByName(a []App) {
	sort.Slice(a, func(i, j int) bool {
		return strings.ToLower(a[i].Name) < strings.ToLower(a[j].Name)
	})
}

// ── USB devices ──────────────────────────────────────────────────────────────

func mapUSBDevices(rows []map[string]any) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		vendor := asString(row["vendor"])
		if vendor == "" {
			vendor = asString(row["manufacturer"])
		}
		model := asString(row["model"])
		if model == "" {
			model = asString(row["product"])
		}
		label := strings.TrimSpace(strings.Join(nonEmpty([]string{vendor, model}), " "))
		if label == "" {
			vid := asString(row["vendor_id"])
			mid := asString(row["model_id"])
			if vid != "" || mid != "" {
				label = strings.Trim(vid+":"+mid, ":")
			}
		}
		if label != "" {
			out = append(out, label)
		}
	}
	return out
}

// ── /etc/hosts ───────────────────────────────────────────────────────────────

func mapEtcHosts(rows []map[string]any) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		address := strings.TrimSpace(asString(row["address"]))
		hosts := strings.TrimSpace(asString(row["hostnames"]))
		if hosts == "" {
			hosts = strings.TrimSpace(asString(row["hostname"]))
		}
		if address != "" && hosts != "" {
			out = append(out, address+" "+hosts)
		}
	}
	return out
}

// ── Policy statuses ──────────────────────────────────────────────────────────

func mapPolicyStatuses(rows []map[string]any) []PolicyStatus {
	out := make([]PolicyStatus, 0, len(rows))
	for _, row := range rows {
		name := asString(row["policyName"])
		if name == "" {
			name = asString(row["name"])
		}
		if name == "" {
			continue
		}
		status := strings.ToLower(asString(row["policyStatus"]))
		if status == "" {
			status = "unknown"
		}
		out = append(out, PolicyStatus{Name: name, Status: status})
	}
	return out
}

// ── Top-level mappers ────────────────────────────────────────────────────────

// MapSystemInput bundles all raw inputs needed to assemble a System.
type MapSystemInput struct {
	Raw               map[string]any
	OwnerEmail        string
	PolicyStatuses    []map[string]any
	AggPolicyStats    map[string]any
	SystemInfo        []map[string]any
	OSVersionSI       []map[string]any
	DiskEnc           []map[string]any
	Bitlocker         []map[string]any
	InterfaceDetails  []map[string]any
	LocalUsers        []map[string]any
	USBDevicesRaw     []map[string]any
	AppsRaw           []map[string]any
	ProgramsRaw       []map[string]any
	DEBRaw            []map[string]any
	RPMRaw            []map[string]any
	BrowserPluginsRaw []map[string]any
	ChromeExtRaw      []map[string]any
	FirefoxAddonsRaw  []map[string]any
	SafariExtRaw      []map[string]any
	EtcHostsRaw       []map[string]any
	Mounts            []map[string]any
	DiskInfo          []map[string]any
	BlockDevices      []map[string]any
}

// MapSystem assembles a System from all enrichment data for one endpoint.
func MapSystem(in MapSystemInput) System {
	raw := in.Raw
	var si map[string]any
	if len(in.SystemInfo) > 0 {
		si = in.SystemInfo[0]
	}
	var osSI map[string]any
	if len(in.OSVersionSI) > 0 {
		osSI = in.OSVersionSI[0]
	}

	// Hardware integers (nilable).
	var ramBytes *int64
	if v := asInt64Ptr(si["physical_memory"]); v != nil {
		ramBytes = v
	}
	cpuPhys := asIntPtr(si["cpu_physical_cores"])
	cpuLog := asIntPtr(si["cpu_logical_cores"])

	// OS.
	osFamily := strings.ToLower(asString(raw["osFamily"]))
	osDetail := asMap(raw["osVersionDetail"])
	verFromDetail := joinNonEmpty(".",
		fmt.Sprintf("%v", normaliseVerComponent(osDetail["major"])),
		fmt.Sprintf("%v", normaliseVerComponent(osDetail["minor"])),
		fmt.Sprintf("%v", normaliseVerComponent(osDetail["patch"])),
	)
	osVersion := asString(osSI["version"])
	if osVersion == "" {
		osVersion = verFromDetail
	}
	if osVersion == "" {
		osVersion = asString(raw["osVersion"])
	}

	mdmRaw := asMap(raw["mdm"])

	// Encryption: SI first, then v1 fde fallback.
	encrypted, encType, fv := detectEncryption(in.DiskEnc, in.Bitlocker)
	if encrypted == nil {
		fde := asMap(raw["fde"])
		if v, ok := fde["fdeEnabled"]; ok {
			if b, isBool := v.(bool); isBool {
				if b {
					t := true
					encrypted = &t
					if encType == "" {
						encType = "FileVault"
					}
				} else {
					f := false
					encrypted = &f
				}
			}
		}
	}

	// Policy statuses + aggregated stats.
	mapped := mapPolicyStatuses(in.PolicyStatuses)
	policyCountData := asMap(in.AggPolicyStats["policyCountData"])
	statsOut := make(map[string]int)
	for k, v := range policyCountData {
		if i := asInt(v); i != 0 {
			statsOut[k] = i
		}
	}
	if len(statsOut) == 0 {
		for k, v := range asMap(raw["policyStats"]) {
			if i := asInt(v); i != 0 {
				statsOut[k] = i
			}
		}
		if len(statsOut) == 0 {
			statsOut = nil
		}
	}

	failedPolicies := nonEmptyAsStrings(in.AggPolicyStats["failedPolicies"])
	pendingPolicies := nonEmptyAsStrings(in.AggPolicyStats["pendingPolicies"])

	// Local users.
	userNames := make([]string, 0, len(in.LocalUsers))
	for _, row := range in.LocalUsers {
		if u := asString(row["username"]); u != "" {
			userNames = append(userNames, u)
		}
	}
	unexpected := make([]string, 0)
	for _, u := range userNames {
		if !isExpectedUser(u) {
			unexpected = append(unexpected, u)
		}
	}

	appliedNames := make([]string, 0, len(mapped))
	for _, p := range mapped {
		appliedNames = append(appliedNames, p.Name)
	}

	return System{
		SystemID:    asString(raw["_id"]),
		Hostname:    asString(raw["hostname"]),
		DisplayName: asString(raw["displayName"]),

		OSType:     asString(raw["os"]),
		OSFamily:   osFamily,
		OSVersion:  osVersion,
		OSCodename: asString(osSI["codename"]),
		OSBuild:    firstNonEmpty(asString(osSI["build"]), asString(osDetail["build"])),

		SerialNumber: asString(raw["serialNumber"]),
		LastContact:  parseTime(asString(raw["lastContact"])),
		Active:       asBool(raw["active"]),
		AgentVersion: asString(raw["agentVersion"]),
		RemoteIP:     asString(raw["remoteIP"]),
		OwnerEmail:   in.OwnerEmail,

		Manufacturer:     firstNonEmpty(asString(si["hardware_vendor"]), asString(raw["hwVendor"])),
		HardwareModel:    asString(si["hardware_model"]),
		HardwareUUID:     asString(si["uuid"]),
		CPUBrand:         asString(si["cpu_brand"]),
		CPUPhysicalCores: cpuPhys,
		CPULogicalCores:  cpuLog,
		RAMBytes:         ramBytes,
		DiskSizeBytes:    diskSizeBytes(osFamily, in.Mounts, in.DiskInfo, in.BlockDevices),

		MACAddresses: extractMACAddresses(in.InterfaceDetails),

		MDMEnrolled:       asString(mdmRaw["enrollmentType"]) != "",
		MDMVendor:         asString(mdmRaw["vendor"]),
		MDMDEP:            asBool(mdmRaw["dep"]),
		MDMUserApproved:   asBool(mdmRaw["userApproved"]),
		MDMEnrollmentType: asString(mdmRaw["enrollmentType"]),

		DiskEncrypted:   encrypted,
		EncryptionType:  encType,
		FileVaultStatus: fv,

		PolicyStats:     statsOut,
		FailedPolicies:  failedPolicies,
		PendingPolicies: pendingPolicies,
		PolicyStatuses:  mapped,
		AppliedPolicies: appliedNames,

		LocalUsers:           userNames,
		UnexpectedLocalUsers: unexpected,

		USBDevices: mapUSBDevices(in.USBDevicesRaw),

		Apps:        mapApps(in.AppsRaw),
		Programs:    mapPrograms(in.ProgramsRaw),
		DEBPackages: mapLinuxPackages(in.DEBRaw),
		RPMPackages: mapLinuxPackages(in.RPMRaw),

		BrowserPlugins:   mapExtensions(in.BrowserPluginsRaw),
		ChromeExtensions: mapExtensions(in.ChromeExtRaw),
		FirefoxAddons:    mapExtensions(in.FirefoxAddonsRaw),
		SafariExtensions: mapExtensions(in.SafariExtRaw),

		EtcHosts: mapEtcHosts(in.EtcHostsRaw),
	}
}

// MapUser builds a User from a /api/systemusers list entry.
func MapUser(raw map[string]any) User {
	mfa := asMap(raw["mfa"])
	firstname := asString(raw["firstname"])
	lastname := asString(raw["lastname"])
	fullName := asString(raw["displayname"])
	if fullName == "" {
		fullName = strings.TrimSpace(firstname + " " + lastname)
	}

	sshRaw := asSlice(raw["ssh_keys"])
	sshKeys := make([]SSHKey, 0, len(sshRaw))
	for _, kr := range sshRaw {
		k := asMap(kr)
		pub := asString(k["public_key"])
		var prefix string
		if pub != "" {
			if len(pub) > 40 {
				prefix = pub[:40]
			} else {
				prefix = pub
			}
		}
		sshKeys = append(sshKeys, SSHKey{
			Name:            asString(k["name"]),
			PublicKeyPrefix: prefix,
			CreatedAt:       parseTime(asString(k["create_date"])),
		})
	}

	pwExp := parseTime(firstNonEmpty(
		asString(raw["password_expiration_date"]),
		asString(raw["passwordExpirationDate"]),
	))

	activated := true
	if v, ok := raw["activated"]; ok {
		activated = asBool(v)
	}

	return User{
		UserID:   asString(raw["_id"]),
		Email:    asString(raw["email"]),
		Username: asString(raw["username"]),
		FullName: fullName,

		MFAConfigured: asBool(mfa["configured"]),
		TOTPEnabled:   asBool(raw["totp_enabled"]),
		MFARequired:   asBool(raw["enable_user_portal_multifactor"]),

		PasswordExpirationDate: pwExp,
		PasswordExpired:        asBool(raw["password_expired"]) || asBool(raw["passwordExpired"]),
		PasswordNeverExpires:   asBool(raw["password_never_expires"]),

		AccountLocked: asBool(raw["account_locked"]) || asBool(raw["accountLocked"]),
		Activated:     activated,
		Suspended:     asBool(raw["suspended"]),

		SSHKeys: sshKeys,
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

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
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// asInt converts a JSON-decoded number/string into int (best-effort).
func asInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case string:
		// rare; JumpCloud occasionally stringifies counts
		var n int
		fmt.Sscanf(x, "%d", &n)
		return n
	}
	return 0
}

func asInt64(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case string:
		var n int64
		fmt.Sscanf(x, "%d", &n)
		return n
	}
	return 0
}

func asIntPtr(v any) *int {
	if v == nil {
		return nil
	}
	if n := asInt(v); n != 0 {
		return &n
	}
	// 0 is a legitimate value; distinguish via type check
	switch v.(type) {
	case float64, int, int64:
		zero := 0
		return &zero
	}
	return nil
}

func asInt64Ptr(v any) *int64 {
	if v == nil {
		return nil
	}
	if n := asInt64(v); n != 0 {
		return &n
	}
	switch v.(type) {
	case float64, int, int64:
		zero := int64(0)
		return &zero
	}
	return nil
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

func nonEmptyAsStrings(v any) []string {
	out := asStringSlice(v)
	if out == nil {
		return nil
	}
	cleaned := out[:0]
	for _, s := range out {
		if s != "" {
			cleaned = append(cleaned, s)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

func intLike(v any, want int) bool {
	switch x := v.(type) {
	case float64:
		return int(x) == want
	case int:
		return x == want
	case int64:
		return int(x) == want
	}
	return false
}

func boolLike(v any, want bool) bool {
	if b, ok := v.(bool); ok {
		return b == want
	}
	return false
}

func stringLike(v any, want string) bool {
	if s, ok := v.(string); ok {
		return s == want
	}
	return false
}

func containsCI(s string, needles ...string) bool {
	low := strings.ToLower(s)
	for _, n := range needles {
		if low == n {
			return true
		}
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

func epochToDateString(v any) string {
	if v == nil {
		return ""
	}
	var sec float64
	switch x := v.(type) {
	case float64:
		sec = x
	case int:
		sec = float64(x)
	case int64:
		sec = float64(x)
	case string:
		var n float64
		_, err := fmt.Sscanf(x, "%f", &n)
		if err != nil {
			return ""
		}
		sec = n
	default:
		return ""
	}
	if sec <= 0 {
		return ""
	}
	t := time.Unix(int64(sec), 0).UTC()
	return t.Format("2006-01-02")
}

func nonEmpty(items []string) []string {
	out := items[:0]
	for _, s := range items {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func joinNonEmpty(sep string, parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" && p != "<nil>" {
			cleaned = append(cleaned, p)
		}
	}
	return strings.Join(cleaned, sep)
}

func firstNonEmpty(parts ...string) string {
	for _, p := range parts {
		if p != "" {
			return p
		}
	}
	return ""
}

func normaliseVerComponent(v any) any {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case float64:
		return int(x)
	}
	return v
}
