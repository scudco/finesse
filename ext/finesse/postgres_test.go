package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// pgFixture is a fresh messages table with the NOTIFY trigger, a registry and
// a LISTEN loop. Skipped unless FINESSE_TEST_PG_URL points at a PostgreSQL 14+
// database.
type pgFixture struct {
	cfg      Config
	db       *sql.DB
	registry *broadcasterRegistry
}

func setupPostgres(t *testing.T) *pgFixture {
	t.Helper()
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
	t.Cleanup(cancel)

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
	t.Cleanup(func() { db.Close() })

	registry := newBroadcasterRegistry(db, cfg)
	t.Cleanup(registry.stopAll)
	go listenNotify(ctx, cfg, registry)

	// Give LISTEN a moment to arm before an insert fires the notification.
	time.Sleep(200 * time.Millisecond)
	return &pgFixture{cfg: cfg, db: db, registry: registry}
}

func (f *pgFixture) insertSQL() string {
	return fmt.Sprintf("INSERT INTO %s (channel, payload) VALUES ($1, $2)", f.cfg.TableName)
}

func TestPostgres_NotifyEndToEnd(t *testing.T) {
	f := setupPostgres(t)

	ch, catchup := f.registry.subscribe("chat", -1)
	defer f.registry.unsubscribe("chat", ch)
	if len(catchup) != 0 {
		t.Fatalf("expected no catchup, got %d", len(catchup))
	}

	start := time.Now()
	if _, err := f.db.Exec(f.insertSQL(), []byte("chat"), []byte(`"<div>from pg</div>"`)); err != nil {
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

// Ids come from a sequence at INSERT, rows appear at COMMIT. When the higher
// id commits first, the lower one must still be delivered once it commits.
func TestPostgres_OutOfOrderCommitDelivered(t *testing.T) {
	f := setupPostgres(t)

	ch, _ := f.registry.subscribe("chat", -1)
	defer f.registry.unsubscribe("chat", ch)

	ctx := context.Background()
	txA, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer txA.Rollback()
	// A takes the lower id and stays open.
	if _, err := txA.Exec(f.insertSQL(), []byte("chat"), []byte(`"<div>A</div>"`)); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	// B takes the next id and commits first, on another connection.
	if _, err := f.db.Exec(f.insertSQL(), []byte("chat"), []byte(`"<div>B</div>"`)); err != nil {
		t.Fatalf("insert B: %v", err)
	}

	got := recvHTML(t, ch, 1)
	if got[0] != "<div>B</div>" {
		t.Fatalf("expected B first, got %v", got)
	}

	if err := txA.Commit(); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	got = recvHTML(t, ch, 1)
	if got[0] != "<div>A</div>" {
		t.Fatalf("expected late A, got %v", got)
	}

	// A reconnect from before both replays each exactly once, in id order.
	ch2, catchup := f.registry.subscribe("chat", 0)
	defer f.registry.unsubscribe("chat", ch2)
	if len(catchup) != 2 || catchup[0].html != "<div>A</div>" || catchup[1].html != "<div>B</div>" {
		t.Fatalf("unexpected replay: %+v", catchup)
	}
}

// recvHTML collects n messages from ch, failing if they do not arrive.
func recvHTML(t *testing.T, ch chan []message, n int) []string {
	t.Helper()
	var out []string
	for len(out) < n {
		select {
		case msgs := <-ch:
			for _, m := range msgs {
				out = append(out, m.html)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("got %d of %d messages: %v", len(out), n, out)
		}
	}
	if len(out) != n {
		t.Fatalf("expected %d messages, got %v", n, out)
	}
	return out
}
