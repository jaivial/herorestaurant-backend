package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"preactvillacarmen/internal/api"
	"preactvillacarmen/internal/config"
	appdb "preactvillacarmen/internal/db"
	"preactvillacarmen/internal/db/migrations"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

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
	srv := newHTTPServer(cfg.Addr, server.Routes())
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	// Install the signal handler before recovery so a SIGTERM during the
	// startup window is not missed (review nit R1).
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	// Campaigns a previous process left in 'sending' are only reported unless
	// CAMPAIGN_AUTO_RESUME=1 (see campaign_runner_durable.go).
	server.RecoverInterruptedCampaigns(context.Background())

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case sig := <-stop:
		log.Printf("Received %v: stopping campaign runners after the delivery in flight", sig)
		api.BeginCampaignShutdown()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancelShutdown()
		_ = srv.Shutdown(shutdownCtx)
		if !api.WaitCampaignRunners(20 * time.Second) {
			log.Printf("Campaign runners did not stop in time; pending rows stay pending")
		}
	}
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
