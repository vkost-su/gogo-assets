package classify

import (
	"fmt"

	"gogo-assets/internal/model"
)

// snapshotRef is the JSON path of the live snapshot, used to build evidence
// pointers Claude can drill into.
const snapshotRef = "local/current/snapshot.json"

// entities flattens a snapshot into the classified entity set: JumpCloud
// devices and users, Sophos endpoints, and Google Workspace users. Rollup
// shards (policy enforcement, account health) and GWS devices are intentionally
// excluded — they are not classified in this version.
//
// Each returned Result has Entity/Service/Val/EvidenceRef populated; the
// classification fields are filled by Run.
func entities(snap model.Snapshot) []Result {
	out := make([]Result, 0,
		len(snap.JumpCloud.Devices)+len(snap.JumpCloud.Identity)+
			len(snap.Sophos.Endpoints)+len(snap.GoogleWorkspace.Identity))

	for _, d := range snap.JumpCloud.Devices {
		out = append(out, Result{
			Entity: model.Entity{
				Type: model.EntityDevice, ID: d.SystemID,
				Hostname: d.Hostname, OwnerEmail: d.OwnerEmail,
			},
			Service:     model.ServiceJumpCloud,
			Val:         d,
			EvidenceRef: evidence("jumpcloud.devices", "system_id", d.SystemID),
		})
	}
	for _, u := range snap.JumpCloud.Identity {
		out = append(out, Result{
			Entity:      model.Entity{Type: model.EntityUser, ID: u.Email, OwnerEmail: u.Email},
			Service:     model.ServiceJumpCloud,
			Val:         u,
			EvidenceRef: evidence("jumpcloud.identity", "email", u.Email),
		})
	}
	for _, e := range snap.Sophos.Endpoints {
		out = append(out, Result{
			Entity: model.Entity{
				Type: model.EntityDevice, ID: e.EndpointID,
				Hostname: e.Hostname, OwnerEmail: e.OwnerEmail,
			},
			Service:     model.ServiceSophos,
			Val:         e,
			EvidenceRef: evidence("sophos.endpoints", "endpoint_id", e.EndpointID),
		})
	}
	for _, u := range snap.GoogleWorkspace.Identity {
		out = append(out, Result{
			Entity:      model.Entity{Type: model.EntityUser, ID: u.Email, OwnerEmail: u.Email},
			Service:     model.ServiceGoogleWorkspace,
			Val:         u,
			EvidenceRef: evidence("google_workspace.identity", "email", u.Email),
		})
	}
	return out
}

func evidence(shard, key, id string) string {
	return fmt.Sprintf("%s#%s[%s=%s]", snapshotRef, shard, key, id)
}
