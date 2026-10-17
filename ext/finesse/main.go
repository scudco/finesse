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

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	cfg := ParseConfig()
	log.Printf("Finesse %s", version)

	db, err := openDB(cfg)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	registry := newBroadcasterRegistry(db, cfg)

	// Shared context cancelled once the drain window ends to close all SSE
	// connections. Broadcasters and the NOTIFY listener keep running until then.
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
		w.Header().Set("X-Finesse-Version", version)
		fmt.Fprint(w, version)
	})
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		sseHandler(w, r, registry, shutdownCtx, cfg)
	})

	addr := fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}
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
	log.Printf("shutting down: draining streams for up to %s (signal again to exit now)", cfg.DrainTimeout)

	if !drain(srv, shutdownCancel, cfg.DrainTimeout, done) {
		log.Println("second signal, exiting now")
		os.Exit(1)
	}

	// Streams are closed; now the broadcasters can stop.
	registry.stopAll()
	log.Println("stopped")
}

// drain stops accepting connections, then gives open SSE streams up to timeout
// to finish on their own. After that it calls endStreams so the handlers return
// and the connections close normally. Streams are never answered with 204/503:
// per the HTML spec any non-200 response makes EventSource stop reconnecting
// for good, whereas a plain close makes the browser reconnect after `retry`
// and resume with Last-Event-ID.
// https://html.spec.whatwg.org/multipage/server-sent-events.html
// It returns false if a signal arrives on abort before draining finishes.
func drain(srv *http.Server, endStreams func(), timeout time.Duration, abort <-chan os.Signal) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	finished := make(chan struct{})
	go func() {
		// Shutdown closes the listeners at once, then waits for streams.
		srv.Shutdown(ctx)
		close(finished)
	}()

	select {
	case <-finished:
		return true
	case <-abort:
		return false
	case <-ctx.Done():
	}

	endStreams()
	// Give the handlers a moment to return and flush their final bytes.
	final, cancelFinal := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelFinal()
	if err := srv.Shutdown(final); err != nil {
		log.Printf("shutdown: %v", err)
	}
	return true
}
