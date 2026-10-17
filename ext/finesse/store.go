package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// notifyChannel is the pg_notify channel the trigger fires and the server
// LISTENs on. Payloads carry only the cable channel name, never the message,
// so the 8000-byte NOTIFY limit cannot apply.
const notifyChannel = "finesse_messages"

// openDB opens the SolidCable messages database for the configured backend.
func openDB(cfg Config) (*sql.DB, error) {
	if cfg.Postgres() {
		return sql.Open("pgx", cfg.DBURL)
	}
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return nil, err
	}
	// WAL mode lets the Go server read while Rails writes concurrently.
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;"); err != nil {
		log.Printf("pragma: %v", err)
	}
	return db, nil
}

// bind rewrites `?` placeholders to `$1..$n` for PostgreSQL.
func bind(query string, postgres bool) string {
	if !postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, c := range query {
		if c == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// setupNotify installs an AFTER INSERT trigger that NOTIFYs each new row's
// channel name, waking that channel's broadcaster without waiting for the
// poll ticker. Requires PostgreSQL 14+ (CREATE OR REPLACE TRIGGER).
func setupNotify(ctx context.Context, cfg Config) error {
	conn, err := pgx.Connect(ctx, cfg.DBURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	// TableName is validated in ParseConfig (alphanumeric and underscores).
	ddl := fmt.Sprintf(`
CREATE OR REPLACE FUNCTION finesse_notify() RETURNS trigger AS $fn$
BEGIN
  PERFORM pg_notify('%s', convert_from(NEW.channel, 'UTF8'));
  RETURN NULL;
END
$fn$ LANGUAGE plpgsql;
CREATE OR REPLACE TRIGGER finesse_notify AFTER INSERT ON %s
  FOR EACH ROW EXECUTE FUNCTION finesse_notify();`, notifyChannel, cfg.TableName)
	_, err = conn.Exec(ctx, ddl)
	return err
}

// listenNotify wakes broadcasters from pg_notify until ctx is cancelled.
// On connection errors it reconnects after 1s; the poll ticker remains the
// safety net for notifications missed while disconnected.
func listenNotify(ctx context.Context, cfg Config, registry *broadcasterRegistry) {
	for {
		if err := waitForNotifications(ctx, cfg, registry); err != nil && ctx.Err() == nil {
			log.Printf("pg listen: %v (reconnecting in 1s)", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func waitForNotifications(ctx context.Context, cfg Config, registry *broadcasterRegistry) error {
	conn, err := pgx.Connect(ctx, cfg.DBURL)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "LISTEN "+notifyChannel); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		registry.wake(n.Payload)
	}
}
