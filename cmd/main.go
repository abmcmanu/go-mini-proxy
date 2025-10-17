package main

import (
	"github.com/abmcmanu/go-mini-proxy/internal"
	"log"
	"os"
)

func main() {
	configPath := "examples/example.conf"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := internal.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Erreur config: %v", err)
	}

	server := internal.NewServer(cfg)
	if err := server.Start(); err != nil {
		log.Fatalf("Erreur serveur: %v", err)
	}
}
