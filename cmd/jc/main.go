// Command jc is a standalone JumpCloud collector.
// It runs the full JumpCloud pipeline (systems, users, SaaS) and optionally
// prints the output as JSON and/or persists it to local/current/jc_raw.json.
//
// Usage:
//
//	jc [--json] [--no-persist]
//
// Flags:
//
//	--json        print collected output as JSON to stdout
//	--no-persist  skip local store write
//
// Required environment variable:
//
//	JC_API_KEY   JumpCloud API key
//
// Optional environment variables:
//
//	JC_ORG_ID           multi-tenant org ID
//	JC_SAAS_USAGE_DAYS  SaaS usage window in days (default 30)
//	JC_MAX_RPS          steady request-rate cap (default: client default)
//	LOCAL_DIR           local storage root (default ./local)
//	LOG_LEVEL           log verbosity (default INFO)
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
		Name:        "jc",
		Module:      service.JumpCloudModule{},
		LoadOptions: config.LoadOptions{RequireJumpCloud: true},
	})
}
