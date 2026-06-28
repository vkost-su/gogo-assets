// Package servicecli contains the shared command runner for standalone service
// binaries such as gw, jc, and sophos.
package servicecli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"gogo-assets/internal/config"
	"gogo-assets/internal/httpstat"
	"gogo-assets/internal/logging"
	"gogo-assets/internal/model"
	"gogo-assets/internal/service"
	"gogo-assets/internal/snapshot"
)

// Options defines one standalone service command. New service binaries should
// normally need only a package main that calls Run with these fields.
type Options struct {
	Name        string
	Module      service.Module
	LoadOptions config.LoadOptions
	Stdout      io.Writer // defaults to os.Stdout
}

// Run executes a standalone service collector binary. It intentionally mirrors
// the behavior shared by all service commands: parse --json/--no-persist, load
// service-scoped config, collect through the module contract, log HTTP/query
// provenance, optionally emit JSON, and optionally persist the raw artifact.
func Run(args []string, opts Options) error {
	if opts.Name == "" {
		return errors.New("service command name is required")
	}
	if opts.Module == nil {
		return errors.New("service module is required")
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	fs := flag.NewFlagSet(opts.Name, flag.ContinueOnError)
	emitJSON := fs.Bool("json", false, "print collected output as JSON to stdout")
	noPersist := fs.Bool("no-persist", false, "skip local store write")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse flags: %w", err)
	}

	logging.Configure("INFO")
	settings, err := config.LoadWithOptions("", opts.LoadOptions)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logging.Configure(settings.LogLevel)
	log := logging.For(string(opts.Module.Key()))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	httpC := httpstat.New()
	result, err := opts.Module.Collect(ctx, service.Runtime{
		Settings:    settings,
		HTTPCounter: httpC,
		Log:         log,
	})
	if err != nil {
		return fmt.Errorf("%s collect: %w", opts.Module.Key(), err)
	}

	log.Info("http requests", httpC.Snapshot().LogArgs()...)
	if q := result.Queries; len(q) > 0 {
		log.Info("api queries",
			"service", apiServiceName(result.Service),
			"count", len(q),
			"endpoints", q)
	}

	if *emitJSON {
		if err := json.NewEncoder(stdout).Encode(result.Output); err != nil {
			return fmt.Errorf("encode output: %w", err)
		}
	}

	if !*noPersist {
		store := snapshot.NewStore(settings.LocalDir)
		if err := store.EnsureDirs(); err != nil {
			return fmt.Errorf("prepare local dir: %w", err)
		}
		res, err := store.WriteCurrentJSON(opts.Module.RawArtifactName(), result.Output)
		if err != nil {
			log.Error("persist failed", "err", err)
		} else {
			log.Info("persisted", "path", res.Path, "size", snapshot.HumanBytes(res.SizeBytes))
		}
	}

	return nil
}

func apiServiceName(s model.Service) string {
	switch s {
	case model.ServiceGoogleWorkspace:
		return "google_workspace"
	case model.ServiceJumpCloud:
		return "jumpcloud"
	case model.ServiceSophos:
		return "sophos"
	default:
		return strings.ToLower(string(s))
	}
}
