package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"preactvillacarmen/internal/api"
	"preactvillacarmen/internal/config"
	appdb "preactvillacarmen/internal/db"
	"preactvillacarmen/internal/db/migrations"
)

func main() {
	cfg := config.Load()

	db, err := appdb.OpenMySQL(cfg.MySQL)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// SKIP_MIGRATIONS=1 starts against an already-migrated database (used by
	// dev/test tooling such as the Forky e2e stub backend).
	if os.Getenv("SKIP_MIGRATIONS") != "1" {
		if err := migrations.Apply(ctx, db); err != nil {
			log.Fatalf("Failed to apply migrations: %v", err)
		}
	}

	server := api.NewServer(db, cfg)
	log.Printf("Hero Restaurant API starting on %s", cfg.Addr)
	log.Fatal(newHTTPServer(cfg.Addr, server.Routes()).ListenAndServe())
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}
