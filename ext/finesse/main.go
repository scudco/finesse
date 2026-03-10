package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	cfg := ParseConfig()

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// WAL mode lets the Go server read while Rails writes concurrently.
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;"); err != nil {
		log.Printf("pragma: %v", err)
	}

	registry := newBroadcasterRegistry(db, cfg)

	// Shared context cancelled on shutdown to close all SSE connections.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	mux := http.NewServeMux()
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		sseHandler(w, r, registry, shutdownCtx, cfg)
	})

	addr := fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	// When Shutdown is called, cancel the shared context so all SSE
	// handlers return immediately — no need to wait for the timeout.
	srv.RegisterOnShutdown(shutdownCancel)

	// Graceful shutdown on SIGINT/SIGTERM.
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Finesse SSE server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-done
	log.Println("shutting down...")

	registry.stopAll()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}

	log.Println("stopped")
}
