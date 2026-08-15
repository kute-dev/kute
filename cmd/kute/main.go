package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/kute-dev/kute/internal/app"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cfg := app.DefaultConfig()
	cfg.Version = version
	cfg.Commit = commit
	cfg.Date = date
	flag.StringVar(&cfg.Context, "context", "", "kubeconfig context to launch against (default: last used, else the kubeconfig's current-context)")
	flag.StringVar(&cfg.Namespace, "namespace", "", "namespace to launch in (default: last used, else the context's own)")
	flag.StringVar(&cfg.Namespace, "n", "", "shorthand for --namespace")
	flag.StringVar(&cfg.Kubeconfig, "kubeconfig", "", "path to the kubeconfig file to use (default: $KUBECONFIG, else ~/.kube/config)")
	flag.BoolVar(&cfg.Demo, "demo", false, "run against an in-memory fake cluster instead of a real one")
	flag.BoolVar(&cfg.Keycast, "keycast", false, "show a recent-keypresses chip (bottom-right) — for demo recording")
	flag.StringVar(&cfg.Theme, "theme", "", "override theme selection: dark|light (default: auto-detect)")
	flag.StringVar(&cfg.LogFile, "log-file", "", "write the error and client-go log stream to this file (for bug reports)")
	flag.BoolVar(&cfg.NoUpdateCheck, "no-update-check", false, "disable the once-per-24h update check")
	showVersion := flag.Bool("version", false, "print version information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("kute %s (%s, %s)\n", version, commit, date)
		return
	}

	if err := app.RunWithConfig(cfg); err != nil {
		// A crash has already printed its own footer, naming the report
		// file — a second "kute: program was killed" line under it would
		// bury the one thing the user is meant to act on.
		if !errors.Is(err, app.ErrCrashed) {
			fmt.Fprintf(os.Stderr, "kute: %v\n", err)
		}
		os.Exit(1)
	}
}
