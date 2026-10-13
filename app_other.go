//go:build !windows

package main

import (
	"log"
)

// runApp on non-Windows systems is a headless build for development: it
// serves the configured actions without a GUI.
func runApp() {
	path := configPath()
	cfg, err := LoadConfig(path)
	if err != nil {
		log.Printf("config %s: %v", path, err)
	}
	b := NewBridge(cfg, path)
	b.Logf = log.Printf
	b.Flex.Logf = log.Printf
	if cfg.RadioIP != "" {
		b.Flex.Connect(cfg.RadioIP)
	}
	t, err := StartTrigger(cfg.Port, b)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listening on 127.0.0.1:%d (config %s)", t.Port(), path)
	select {}
}
