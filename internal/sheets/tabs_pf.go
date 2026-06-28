package sheets

import (
	"context"

	"gogo-assets/internal/inventory"
	"gogo-assets/internal/peopleforce"
)

// WritePeopleForce writes the PeopleForce Assets tab.
//
// One row per asset, sorted as stored in AssetInventory.PFAssets (by asset ID
// from the collector). Assigned assets float to the top because the collector
// sorts them into the slice order determined by the PeopleForce API.
func WritePeopleForce(ctx context.Context, svc *Service, sheetName string, inv *inventory.AssetInventory) error {
	cols := pfColumns()
	return writeTab(ctx, svc, sheetName, cols, inv.PFAssets, WriteOptions{})
}

func pfColumns() []Column[peopleforce.Asset] {
	return []Column[peopleforce.Asset]{
		// ── Asset ────────────────────────────────────────────────────────────
		{
			Group:   "Asset",
			Header:  "Category",
			Extract: func(a peopleforce.Asset) string { return a.CategoryName },
		},
		{
			Group:   "Asset",
			Header:  "Name",
			Extract: func(a peopleforce.Asset) string { return a.Name },
		},
		{
			Group:   "Asset",
			Header:  "Code",
			Extract: func(a peopleforce.Asset) string { return a.Code },
		},
		{
			Group:   "Asset",
			Header:  "Serial",
			Extract: func(a peopleforce.Asset) string { return a.SerialNumber },
		},
		{
			Group:   "Asset",
			Header:  "Description",
			Extract: func(a peopleforce.Asset) string { return a.Description },
		},
		// ── Assignment ───────────────────────────────────────────────────────
		{
			Group:  "Assignment",
			Header: "Status",
			Extract: func(a peopleforce.Asset) string {
				if a.IsAssigned {
					return "Assigned"
				}
				return "Unassigned"
			},
			AlertYellow: func(s string) bool { return s == "Unassigned" },
		},
		{
			Group:   "Assignment",
			Header:  "Assigned To",
			Extract: func(a peopleforce.Asset) string { return a.AssignedEmail },
		},
		{
			Group:   "Assignment",
			Header:  "Name",
			Extract: func(a peopleforce.Asset) string { return a.AssignedName },
		},
		{
			Group:   "Assignment",
			Header:  "Issued On",
			Extract: func(a peopleforce.Asset) string { return a.IssuedOn },
		},
		// ── Employee context ─────────────────────────────────────────────────
		{
			Group:   "Employee",
			Header:  "Department",
			Extract: func(a peopleforce.Asset) string { return a.Department },
		},
		{
			Group:   "Employee",
			Header:  "Position",
			Extract: func(a peopleforce.Asset) string { return a.Position },
		},
		{
			Group:   "Employee",
			Header:  "Location",
			Extract: func(a peopleforce.Asset) string { return a.Location },
		},
		// ── Meta ─────────────────────────────────────────────────────────────
		{
			Group:   "Meta",
			Header:  "Asset ID",
			Extract: func(a peopleforce.Asset) string { return a.ID },
		},
		{
			Group:  "Meta",
			Header: "Created",
			Extract: func(a peopleforce.Asset) string {
				if a.CreatedAt.IsZero() {
					return ""
				}
				return a.CreatedAt.Format("2006-01-02")
			},
		},
	}
}
