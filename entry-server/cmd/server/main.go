package main

import (
	"log"
	"os"

	entryserver "framefleet/entry-server/internal/server"
	"framefleet/entry-server/internal/service"
)

func main() {
	addr := os.Getenv("ENTRY_SERVER_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	workerRegistry := service.NewWorkerRegistry()
	server := entryserver.New(workerRegistry)

	if err := server.Run(addr); err != nil {
		log.Fatalf("entry server stopped: %v", err)
	}
}
