package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	cfg := ParseConfig()

	db, err := openDB(cfg)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	registry := newBroadcasterRegistry(db, cfg)

	// Shared context cancelled on shutdown to close all SSE connections.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	// Postgres: a trigger NOTIFYs each insert's channel so broadcasters poll
	// immediately instead of waiting out the slower poll interval.
	if cfg.Postgres() {
		if err := setupNotify(shutdownCtx, cfg); err != nil {
			log.Printf("pg_notify setup failed: %v; relying on polling every %s", err, cfg.PollInterval)
		} else {
			go listenNotify(shutdownCtx, cfg, registry)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		if origin := matchOrigin(r.Header.Get("Origin"), cfg.AllowOrigins); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
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
