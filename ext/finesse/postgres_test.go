package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

	got := recvMessages(t, ch, 1)
	if got[0].html != "<div>B</div>" || got[0].late {
		t.Fatalf("expected B first and on time, got %+v", got)
	}

	if err := txA.Commit(); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	got = recvMessages(t, ch, 1)
	if got[0].html != "<div>A</div>" || !got[0].late {
		t.Fatalf("expected A, marked late, got %+v", got)
	}

	// A reconnect from before both replays each exactly once, in id order, with
	// ids (that client never saw a higher one).
	ch2, catchup := f.registry.subscribe("chat", 0)
	defer f.registry.unsubscribe("chat", ch2)
	if len(catchup) != 2 || catchup[0].html != "<div>A</div>" || catchup[1].html != "<div>B</div>" || catchup[0].late {
		t.Fatalf("unexpected replay: %+v", catchup)
	}
}

// recvMessages collects n messages from ch, failing if they do not arrive.
func recvMessages(t *testing.T, ch chan []message, n int) []message {
	t.Helper()
	var out []message
	for len(out) < n {
		select {
		case msgs := <-ch:
			out = append(out, msgs...)
		case <-time.After(5 * time.Second):
			t.Fatalf("got %d of %d messages: %v", len(out), n, out)
		}
	}
	if len(out) != n {
		t.Fatalf("expected %d messages, got %v", n, out)
	}
	return out
}

// A late row goes out without an id: line, so the client's Last-Event-ID stays
// at the highest id and a reconnect does not replay rows it already has.
func TestPostgres_LateRowHasNoIDAndReconnectDoesNotReplay(t *testing.T) {
	f := setupPostgres(t)
	cfg := f.cfg
	cfg.SigningKeys = [][]byte{testKey}
	cfg.AllowOrigins = []string{"*"}
	shutdown, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseHandler(w, r, f.registry, shutdown, cfg)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(cancel) // runs first: ends open streams so srv.Close does not block

	open := func(lastID string) *bufio.Reader {
		req, _ := http.NewRequest("GET", srv.URL+"/events?signed_stream="+signStream("chat", testKey), nil)
		if lastID != "" {
			req.Header.Set("Last-Event-ID", lastID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return bufio.NewReader(resp.Body)
	}
	// frame reads one event, up to the blank line.
	frame := func(r *bufio.Reader) string {
		var sb strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("read: %v (so far %q)", err, sb.String())
			}
			sb.WriteString(line)
			if line == "\n" {
				return sb.String()
			}
		}
	}
	insert := func(q interface {
		Exec(string, ...any) (sql.Result, error)
	}, html string) {
		if _, err := q.Exec(f.insertSQL(), []byte("chat"), []byte(`"`+html+`"`)); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	stream := open("")
	time.Sleep(100 * time.Millisecond) // let the handler subscribe

	txA, err := f.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer txA.Rollback()
	insert(txA, "<div>A</div>")  // id 1, held open
	insert(f.db, "<div>B</div>") // id 2, commits first

	if got := frame(stream); got != "id: 2\ndata: <div>B</div>\n\n" {
		t.Fatalf("B frame = %q", got)
	}
	if err := txA.Commit(); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	if got := frame(stream); got != "data: <div>A</div>\n\n" {
		t.Fatalf("late A frame = %q, want no id line", got)
	}

	// The client's last id is 2. Reconnecting from it replays nothing.
	reconnected := open("2")
	time.Sleep(100 * time.Millisecond)
	insert(f.db, "<div>C</div>")
	if got := frame(reconnected); got != "id: 3\ndata: <div>C</div>\n\n" {
		t.Fatalf("first frame after reconnect = %q, want only C", got)
	}
}
