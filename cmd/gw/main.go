// Command gw is a standalone Google Workspace collector.
// It runs the full GWS pipeline and optionally prints the raw service output as
// JSON and/or persists it to local/current/gws_raw.json.
//
// Usage:
//
//	gw [--json] [--no-persist]
//
// Flags:
//
//	--json        print collected records as JSON to stdout
//	--no-persist  skip local store write
//
// Configuration is read from the same environment variables and .env file as
// the orchestrator binary (GWS_SA_JSON_PATH, GWS_ADMIN_EMAIL, etc.).
package main

import (
	"fmt"
	"os"

	"gogo-assets/internal/config"
	"gogo-assets/internal/service"
	"gogo-assets/internal/servicecli"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	return servicecli.Run(args, servicecli.Options{
		Name:        "gw",
		Module:      service.GoogleWorkspaceModule{},
		LoadOptions: config.LoadOptions{RequireGoogle: true},
	})
}
