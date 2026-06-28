package peopleforce

import (
	"time"

	"gogo-assets/internal/model"
)

// ToAsset converts a collected Asset into the canonical PFAsset.
//
// PFAsset carries no monitored fields in this version — it is stored for the
// Assets tab and snapshot drill-down but is not classified by the drift engine.
func ToAsset(a Asset, meta model.Meta) model.PFAsset {
	meta.SourceAPI = "peopleforce.assets"
	return model.PFAsset{
		Meta:            meta,
		AssetID:         a.ID,
		Name:            a.Name,
		Code:            a.Code,
		SerialNumber:    a.SerialNumber,
		Category:        a.CategoryName,
		Description:     a.Description,
		AssignedToEmail: a.AssignedEmail,
		AssignedToName:  a.AssignedName,
		AssignedToID:    a.AssignedToID,
		IssuedOn:        a.IssuedOn,
		Department:      a.Department,
		Position:        a.Position,
		Location:        a.Location,
		IsAssigned:      a.IsAssigned,
		CreatedAt:       ptrTime(a.CreatedAt),
		UpdatedAt:       ptrTime(a.UpdatedAt),
	}
}

func ptrTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
