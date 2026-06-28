// Command pf is a standalone PeopleForce asset collector.
// It fetches all hardware assets from the PeopleForce API, resolves employee
// assignments, and optionally prints the enriched output as JSON and/or
// persists it to local/current/pf_raw.json.
//
// Usage:
//
//	pf [--json] [--no-persist]
//
// Flags:
//
//	--json        print collected assets as JSON to stdout
//	--no-persist  skip local store write
//
// Configuration is read from the same environment variables and .env file as
// the orchestrator binary (PF_API_KEY, PF_BASE_URL, PF_MAX_RPS).
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
		Name:        "pf",
		Module:      service.PeopleForceModule{},
		LoadOptions: config.LoadOptions{RequirePeopleForce: true},
	})
}
