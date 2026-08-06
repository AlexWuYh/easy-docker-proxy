// Command proxy is the easy-docker-proxy entrypoint.
//
// Scaffold stage (M0): validates flags and prints design status.
// Subsequent milestones implement the registry data plane, pull recording, and stats UI.
// See .ai/01_DESIGN.md.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "path to config YAML")
	flag.Parse()

	if _, err := os.Stat(*configPath); err != nil {
		log.Printf("warning: config not found at %s (copy configs/config.example.yaml)", *configPath)
	}

	token := os.Getenv("PROXY_ADMIN_TOKEN")
	if token == "" {
		log.Println("warning: PROXY_ADMIN_TOKEN is unset; admin/stats must fail-closed when implemented")
	}

	fmt.Println("easy-docker-proxy — scaffold (M0)")
	fmt.Println("  config:", *configPath)
	fmt.Println("  design: .ai/01_DESIGN.md")
	fmt.Println("  status: registry proxy / pull records / stats UI not implemented yet")
	fmt.Println()
	fmt.Println("Next: implement internal/proxy (M1). See .ai/00_PROJECT.md")
}
