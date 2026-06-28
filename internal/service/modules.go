package service

import (
	"context"

	"gogo-assets/internal/assemble"
	"gogo-assets/internal/config"
	"gogo-assets/internal/gworkspace"
	"gogo-assets/internal/inventory"
	"gogo-assets/internal/jumpcloud"
	"gogo-assets/internal/model"
	"gogo-assets/internal/peopleforce"
	"gogo-assets/internal/sophos"
)

// GoogleWorkspaceModule wires the Google Workspace collector into the uniform
// service contract. Google Workspace is required because it supplies the primary
// identity spine and Sheets credentials.
type GoogleWorkspaceModule struct{}

func (GoogleWorkspaceModule) Key() Key                    { return KeyGoogleWorkspace }
func (GoogleWorkspaceModule) DisplayName() string         { return "Google Workspace" }
func (GoogleWorkspaceModule) ModelService() model.Service { return model.ServiceGoogleWorkspace }
func (GoogleWorkspaceModule) Required() bool              { return true }
func (GoogleWorkspaceModule) RawArtifactName() string     { return "gws_raw.json" }
func (GoogleWorkspaceModule) MissingConfigMessage() string {
	return "Google Workspace credentials not set"
}
func (GoogleWorkspaceModule) Configured(s config.Settings) bool {
	return s.Google.SAJSONPath != "" && s.Google.AdminEmail != ""
}

func (m GoogleWorkspaceModule) Collect(ctx context.Context, rt Runtime) (Result, error) {
	client, err := gworkspace.New(rt.Settings.Google.SAJSONPath, rt.Settings.Google.AdminEmail, rt.Settings.Google.CustomerID, rt.HTTPCounter)
	if err != nil {
		return Result{}, err
	}
	out := &gworkspace.Output{}
	c := gworkspace.NewCollector(client, out, gworkspace.CollectorOpts{EnrichDelay: rt.Settings.EnrichDelay})
	if err := c.CollectAll(ctx); err != nil {
		return Result{}, err
	}
	return Result{
		Key:         m.Key(),
		Service:     m.ModelService(),
		DisplayName: m.DisplayName(),
		Output:      out,
		Queries:     out.Queries,
		Counts:      map[string]int{"users": len(out.Records)},
	}, nil
}

func (m GoogleWorkspaceModule) IngestInventory(inv *inventory.AssetInventory, r Result) error {
	out, err := outputAs[gworkspace.Output](m, r)
	if err != nil {
		return err
	}
	inv.AddGoogle(out.Records)
	return nil
}

func (m GoogleWorkspaceModule) AppendSources(src *assemble.Sources, r Result) error {
	out, err := outputAs[gworkspace.Output](m, r)
	if err != nil {
		return err
	}
	src.GWS = out.Records
	src.GWSQueries = out.Queries
	return nil
}

// JumpCloudModule wires JumpCloud systems, users, and SaaS App Management into
// the uniform service contract. Missing credentials skip the module.
type JumpCloudModule struct{}

func (JumpCloudModule) Key() Key                    { return KeyJumpCloud }
func (JumpCloudModule) DisplayName() string         { return "JumpCloud" }
func (JumpCloudModule) ModelService() model.Service { return model.ServiceJumpCloud }
func (JumpCloudModule) Required() bool              { return false }
func (JumpCloudModule) RawArtifactName() string     { return "jc_raw.json" }
func (JumpCloudModule) MissingConfigMessage() string {
	return "JC_API_KEY not set — skipping JumpCloud"
}
func (JumpCloudModule) Configured(s config.Settings) bool { return s.JumpCloud.APIKey != "" }

func (m JumpCloudModule) Collect(ctx context.Context, rt Runtime) (Result, error) {
	client := jumpcloud.New(rt.Settings.JumpCloud.APIKey, rt.Settings.JumpCloud.OrgID, rt.Settings.JumpCloud.MaxRPS, rt.HTTPCounter)
	out := &jumpcloud.Output{}
	c := jumpcloud.NewCollector(client, out, jumpcloud.CollectorOpts{SaaSUsageDays: rt.Settings.JumpCloud.SaaSUsageDays})
	if err := c.CollectAll(ctx); err != nil {
		return Result{}, err
	}
	return Result{
		Key:         m.Key(),
		Service:     m.ModelService(),
		DisplayName: m.DisplayName(),
		Output:      out,
		Queries:     out.Queries,
		Counts: map[string]int{
			"systems":   len(out.Systems),
			"users":     len(out.Users),
			"saas_apps": len(out.SaaSApps),
		},
	}, nil
}

func (m JumpCloudModule) IngestInventory(inv *inventory.AssetInventory, r Result) error {
	out, err := outputAs[jumpcloud.Output](m, r)
	if err != nil {
		return err
	}
	inv.AddJC(out.Systems, out.Users)
	inv.SaaSApps = out.SaaSApps
	return nil
}

func (m JumpCloudModule) AppendSources(src *assemble.Sources, r Result) error {
	out, err := outputAs[jumpcloud.Output](m, r)
	if err != nil {
		return err
	}
	src.JCSystems = out.Systems
	src.JCUsers = out.Users
	src.JCSaaS = out.SaaSApps
	src.JCQueries = out.Queries
	return nil
}

// SophosModule wires Sophos Central endpoint collection into the uniform service
// contract. Missing credentials skip the module.
type SophosModule struct{}

func (SophosModule) Key() Key                    { return KeySophos }
func (SophosModule) DisplayName() string         { return "Sophos" }
func (SophosModule) ModelService() model.Service { return model.ServiceSophos }
func (SophosModule) Required() bool              { return false }
func (SophosModule) RawArtifactName() string     { return "sophos_raw.json" }
func (SophosModule) MissingConfigMessage() string {
	return "SOPHOS_CLIENT_ID/SECRET not set — skipping Sophos"
}
func (SophosModule) Configured(s config.Settings) bool {
	return s.Sophos.ClientID != "" && s.Sophos.ClientSecret != ""
}

func (m SophosModule) Collect(ctx context.Context, rt Runtime) (Result, error) {
	client := sophos.New(rt.Settings.Sophos.ClientID, rt.Settings.Sophos.ClientSecret, rt.HTTPCounter)
	out := &sophos.Output{}
	c := sophos.NewCollector(client, out)
	if err := c.CollectAll(ctx); err != nil {
		return Result{}, err
	}
	return Result{
		Key:         m.Key(),
		Service:     m.ModelService(),
		DisplayName: m.DisplayName(),
		Output:      out,
		Queries:     out.Queries,
		Counts:      map[string]int{"endpoints": len(out.Endpoints)},
	}, nil
}

func (m SophosModule) IngestInventory(inv *inventory.AssetInventory, r Result) error {
	out, err := outputAs[sophos.Output](m, r)
	if err != nil {
		return err
	}
	inv.AddSophos(out.Endpoints)
	return nil
}

func (m SophosModule) AppendSources(src *assemble.Sources, r Result) error {
	out, err := outputAs[sophos.Output](m, r)
	if err != nil {
		return err
	}
	src.Endpoints = out.Endpoints
	src.SophosQueries = out.Queries
	return nil
}

// PeopleForceModule wires PeopleForce Asset Management into the uniform service
// contract. Missing credentials skip the module silently.
type PeopleForceModule struct{}

func (PeopleForceModule) Key() Key                    { return KeyPeopleForce }
func (PeopleForceModule) DisplayName() string         { return "PeopleForce" }
func (PeopleForceModule) ModelService() model.Service { return model.ServicePeopleForce }
func (PeopleForceModule) Required() bool              { return false }
func (PeopleForceModule) RawArtifactName() string     { return "pf_raw.json" }
func (PeopleForceModule) MissingConfigMessage() string {
	return "PF_API_KEY not set — skipping PeopleForce"
}
func (PeopleForceModule) Configured(s config.Settings) bool { return s.PeopleForce.APIKey != "" }

func (m PeopleForceModule) Collect(ctx context.Context, rt Runtime) (Result, error) {
	client := peopleforce.New(
		rt.Settings.PeopleForce.APIKey,
		rt.Settings.PeopleForce.BaseURL,
		rt.Settings.PeopleForce.MaxRPS,
		rt.HTTPCounter,
	)
	out := &peopleforce.Output{}
	c := peopleforce.NewCollector(client, out)
	if err := c.CollectAll(ctx); err != nil {
		return Result{}, err
	}
	return Result{
		Key:         m.Key(),
		Service:     m.ModelService(),
		DisplayName: m.DisplayName(),
		Output:      out,
		Queries:     out.Queries,
		Counts:      map[string]int{"assets": len(out.Assets)},
	}, nil
}

func (m PeopleForceModule) IngestInventory(inv *inventory.AssetInventory, r Result) error {
	out, err := outputAs[peopleforce.Output](m, r)
	if err != nil {
		return err
	}
	inv.AddPeopleForce(out.Assets)
	return nil
}

func (m PeopleForceModule) AppendSources(src *assemble.Sources, r Result) error {
	out, err := outputAs[peopleforce.Output](m, r)
	if err != nil {
		return err
	}
	src.PFAssets = out.Assets
	src.PFQueries = out.Queries
	return nil
}
