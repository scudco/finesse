package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestPostgres_NotifyEndToEnd exercises the real PostgreSQL path: trigger
// install, LISTEN, insert, wake, poll, delivery. Skipped unless
// FINESSE_TEST_PG_URL points at a PostgreSQL 14+ database.
func TestPostgres_NotifyEndToEnd(t *testing.T) {
	url := os.Getenv("FINESSE_TEST_PG_URL")
	if url == "" {
		t.Skip("FINESSE_TEST_PG_URL not set")
	}

	cfg := Config{
		DBURL:        url,
		TableName:    "finesse_test_messages",
		PollInterval: time.Hour, // delivery within the deadline proves NOTIFY, not polling
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { admin.Close(context.Background()) })

	if _, err := admin.Exec(ctx, fmt.Sprintf(`
		DROP TABLE IF EXISTS %s;
		CREATE TABLE %s (
			id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			channel BYTEA NOT NULL,
			payload BYTEA NOT NULL
		)`, cfg.TableName, cfg.TableName)); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() {
		// Separate Execs: one multi-statement Exec is an implicit transaction,
		// so a failing DROP FUNCTION (dependent triggers elsewhere) would roll
		// back the table drop too.
		admin.Exec(context.Background(), "DROP TABLE IF EXISTS "+cfg.TableName)
		admin.Exec(context.Background(), "DROP FUNCTION IF EXISTS finesse_notify")
	})

	if err := setupNotify(ctx, cfg); err != nil {
		t.Fatalf("setupNotify: %v", err)
	}

	db, err := openDB(cfg)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	defer db.Close()

	registry := newBroadcasterRegistry(db, cfg)
	defer registry.stopAll()
	go listenNotify(ctx, cfg, registry)

	// Give LISTEN a moment to arm before the insert fires the notification.
	time.Sleep(200 * time.Millisecond)

	ch, catchup := registry.subscribe("chat", -1)
	defer registry.unsubscribe("chat", ch)
	if len(catchup) != 0 {
		t.Fatalf("expected no catchup, got %d", len(catchup))
	}

	start := time.Now()
	if _, err := admin.Exec(ctx,
		fmt.Sprintf("INSERT INTO %s (channel, payload) VALUES ($1, $2)", cfg.TableName),
		[]byte("chat"), []byte(`"<div>from pg</div>"`)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	select {
	case msgs := <-ch:
		if len(msgs) != 1 || msgs[0].html != "<div>from pg</div>" {
			t.Fatalf("unexpected messages: %+v", msgs)
		}
		t.Logf("NOTIFY delivery latency: %s", time.Since(start))
	case <-time.After(5 * time.Second):
		t.Fatal("message was not delivered via NOTIFY")
	}
}
