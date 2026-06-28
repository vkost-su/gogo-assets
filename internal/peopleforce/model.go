// Package peopleforce collects asset and employee data from PeopleForce
// Asset Management in read-only mode. It exposes physical assets (laptops,
// monitors, peripherals, …) and their current assignments so that every managed
// device can be cross-referenced against the JumpCloud / Sophos device records.
package peopleforce

import "time"

// Asset is one physical asset from PeopleForce Asset Management.
// Assignments holds the full history; the collector resolves the current active
// assignment (the most recent record with an empty ReturnedOn) into the
// AssignedTo* / IssuedOn convenience fields.
type Asset struct {
	ID           string
	Name         string
	Code         string
	SerialNumber string
	Description  string
	LocationID   string
	CategoryID   string
	CategoryName string // resolved from AssetCategory.Name

	Assignments []AssetAssignment

	// Resolved from the current (active) assignment + employee directory.
	AssignedToID  int
	AssignedEmail string
	AssignedName  string
	Department    string
	Position      string
	Location      string
	IssuedOn      string // ISO date, e.g. "2024-01-15"
	IsAssigned    bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// AssetAssignment is one historical assignment record within an Asset.
type AssetAssignment struct {
	ID         string
	UserID     int
	AssetID    int
	IssuedOn   string // ISO date; empty = unknown
	ReturnedOn string // ISO date; empty = still active
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Employee is a PeopleForce employee record loaded for assignment resolution.
// Only the fields needed for the assets tab are kept.
type Employee struct {
	ID             int
	FullName       string
	Email          string
	EmployeeNumber string
	Active         bool
	Department     string
	Position       string
	Location       string
}

// AssetCategory is a PeopleForce asset category.
type AssetCategory struct {
	ID   string
	Name string
}

// Output holds the results of a completed PeopleForce collection.
type Output struct {
	Assets  []Asset  `json:"assets"`
	Queries []string `json:"queries"`
}
