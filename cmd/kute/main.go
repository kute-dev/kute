package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kute-dev/kute/internal/app"
)

func main() {
	cfg := app.DefaultConfig()
	flag.BoolVar(&cfg.Demo, "demo", false, "run against an in-memory fake cluster instead of a real one")
	flag.StringVar(&cfg.Theme, "theme", "", "override theme selection: dark|light (default: auto-detect)")
	flag.Parse()

	if err := app.RunWithConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "kute: %v\n", err)
		os.Exit(1)
	}
}
