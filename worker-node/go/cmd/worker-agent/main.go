package main

import (
	"log"

	"framefleet/pkg/envfile"
	"framefleet/worker-node/go/internal/agent"
	"framefleet/worker-node/go/internal/config"
)

func main() {
	if err := envfile.LoadWithOverride("WORKER_ENV_FILE", ".env", "worker-node/.env"); err != nil {
		log.Fatalf("load worker env failed: %v", err)
	}

	cfg := config.FromEnv()

	app, err := agent.New(cfg)
	if err != nil {
		log.Fatalf("initialize worker agent failed: %v", err)
	}

	if err := app.Run(); err != nil {
		log.Fatalf("worker agent stopped: %v", err)
	}
}
