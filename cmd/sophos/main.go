// Command sophos is a standalone Sophos Central collector.
// It runs the full Sophos pipeline and optionally prints the output as JSON
// and/or persists it to local/current/sophos_raw.json.
//
// Usage:
//
//	sophos [--json] [--no-persist]
//
// Flags:
//
//	--json        print collected output as JSON to stdout
//	--no-persist  skip local store write
//
// Required environment variables:
//
//	SOPHOS_CLIENT_ID      Sophos Central OAuth2 client ID
//	SOPHOS_CLIENT_SECRET  Sophos Central OAuth2 client secret
//
// Optional environment variables:
//
//	LOCAL_DIR   local storage root (default ./local)
//	LOG_LEVEL   log verbosity (default INFO)
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
		Name:        "sophos",
		Module:      service.SophosModule{},
		LoadOptions: config.LoadOptions{RequireSophos: true},
	})
}
