package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupTestDB creates an in-memory SQLite database with the solid_cable_messages
// table and returns the db handle plus a cleanup function.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE solid_cable_messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel TEXT NOT NULL,
		payload TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertMessage(t *testing.T, db *sql.DB, channel, html string) int64 {
	t.Helper()
	payload, _ := json.Marshal(html)
	res, err := db.Exec(
		"INSERT INTO solid_cable_messages (channel, payload) VALUES (?, ?)",
		[]byte(channel), payload,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func testConfig() Config {
	return Config{
		TableName:    "solid_cable_messages",
		PollInterval: 5 * time.Millisecond,
	}
}

func TestBroadcaster_SubscribeFreshConnection(t *testing.T) {
	db := setupTestDB(t)
	cfg := testConfig()

	// Insert a message before creating the broadcaster.
	insertMessage(t, db, "chat", "<div>old</div>")

	b := newBroadcaster(db, "chat", cfg)
	defer b.stop()

	// Fresh connection (lastID = -1) should get no catchup.
	ch, catchup := b.subscribe(-1)
	defer b.unsubscribe(ch)

	if len(catchup) != 0 {
		t.Fatalf("expected no catchup for fresh connection, got %d", len(catchup))
	}
}

func TestBroadcaster_SubscribeWithLastID(t *testing.T) {
	db := setupTestDB(t)
	cfg := testConfig()

	b := newBroadcaster(db, "chat", cfg)
	defer b.stop()

	// Insert after broadcaster is created so the poll loop picks them up.
	id1 := insertMessage(t, db, "chat", "<div>first</div>")
	insertMessage(t, db, "chat", "<div>second</div>")

	// Wait for poll to buffer the messages.
	time.Sleep(30 * time.Millisecond)

	// Reconnect with last seen id1 — should get second message as catchup.
	ch, catchup := b.subscribe(id1)
	defer b.unsubscribe(ch)

	if len(catchup) != 1 {
		t.Fatalf("expected 1 catchup message, got %d", len(catchup))
	}
	if catchup[0].html != "<div>second</div>" {
		t.Fatalf("unexpected catchup html: %q", catchup[0].html)
	}
}

func TestBroadcaster_LiveDelivery(t *testing.T) {
	db := setupTestDB(t)
	cfg := testConfig()

	b := newBroadcaster(db, "chat", cfg)
	t.Cleanup(b.stop) // stop before db.Close()

	ch, _ := b.subscribe(-1)
	defer b.unsubscribe(ch)

	// Insert after subscribing.
	insertMessage(t, db, "chat", "<div>live</div>")

	select {
	case msgs := <-ch:
		if len(msgs) != 1 || msgs[0].html != "<div>live</div>" {
			t.Fatalf("unexpected message: %+v", msgs)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for live message")
	}
}

func TestBroadcaster_ChannelIsolation(t *testing.T) {
	db := setupTestDB(t)
	cfg := testConfig()

	b := newBroadcaster(db, "chat", cfg)
	defer b.stop()

	ch, _ := b.subscribe(-1)
	defer b.unsubscribe(ch)

	// Insert to a different channel.
	insertMessage(t, db, "other", "<div>wrong channel</div>")

	select {
	case msgs := <-ch:
		t.Fatalf("should not receive messages from other channel: %+v", msgs)
	case <-time.After(30 * time.Millisecond):
		// Expected: no message received.
	}
}

func TestBroadcaster_MultipleClients(t *testing.T) {
	db := setupTestDB(t)
	cfg := testConfig()

	b := newBroadcaster(db, "chat", cfg)
	defer b.stop()

	ch1, _ := b.subscribe(-1)
	defer b.unsubscribe(ch1)
	ch2, _ := b.subscribe(-1)
	defer b.unsubscribe(ch2)

	insertMessage(t, db, "chat", "<div>broadcast</div>")

	for i, ch := range []chan []message{ch1, ch2} {
		select {
		case msgs := <-ch:
			if msgs[0].html != "<div>broadcast</div>" {
				t.Fatalf("client %d: unexpected html: %q", i, msgs[0].html)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("client %d: timed out", i)
		}
	}
}

func TestBroadcaster_Unsubscribe(t *testing.T) {
	db := setupTestDB(t)
	cfg := testConfig()

	b := newBroadcaster(db, "chat", cfg)
	defer b.stop()

	ch, _ := b.subscribe(-1)
	b.unsubscribe(ch)

	b.mu.Lock()
	count := len(b.clients)
	b.mu.Unlock()

	if count != 0 {
		t.Fatalf("expected 0 clients after unsubscribe, got %d", count)
	}
}

func TestBroadcaster_BufferLimit(t *testing.T) {
	db := setupTestDB(t)
	cfg := testConfig()

	// Insert more than bufferSize messages.
	for i := 0; i < bufferSize+10; i++ {
		insertMessage(t, db, "chat", fmt.Sprintf("<div>msg-%d</div>", i))
	}

	b := newBroadcaster(db, "chat", cfg)
	defer b.stop()

	// Wait for poll.
	time.Sleep(30 * time.Millisecond)

	b.mu.Lock()
	bufLen := len(b.buffer)
	b.mu.Unlock()

	if bufLen > bufferSize {
		t.Fatalf("buffer exceeded max size: %d > %d", bufLen, bufferSize)
	}
}

func TestBroadcasterRegistry_SubscribeGetOrCreate(t *testing.T) {
	db := setupTestDB(t)
	cfg := testConfig()

	r := newBroadcasterRegistry(db, cfg)
	defer r.stopAll()

	r.subscribe("chat", -1)
	r.subscribe("chat", -1)
	r.subscribe("other", -1)

	r.mu.Lock()
	count := len(r.broadcasters)
	chatClients := len(r.broadcasters["chat"].clients)
	r.mu.Unlock()

	if count != 2 {
		t.Fatalf("expected 2 broadcasters, got %d", count)
	}
	if chatClients != 2 {
		t.Fatalf("expected 2 clients on shared broadcaster, got %d", chatClients)
	}
}

func TestBroadcasterRegistry_ReapOnLastUnsubscribe(t *testing.T) {
	db := setupTestDB(t)
	cfg := testConfig()

	r := newBroadcasterRegistry(db, cfg)
	defer r.stopAll()

	ch1, _ := r.subscribe("chat", -1)
	ch2, _ := r.subscribe("chat", -1)

	r.mu.Lock()
	b := r.broadcasters["chat"]
	r.mu.Unlock()

	r.unsubscribe("chat", ch1)

	r.mu.Lock()
	_, ok := r.broadcasters["chat"]
	r.mu.Unlock()
	if !ok {
		t.Fatal("broadcaster reaped while a client remained")
	}

	r.unsubscribe("chat", ch2)

	r.mu.Lock()
	_, ok = r.broadcasters["chat"]
	r.mu.Unlock()
	if ok {
		t.Fatal("broadcaster still registered after last unsubscribe")
	}

	select {
	case <-b.doneCh:
	case <-time.After(time.Second):
		t.Fatal("poll goroutine still running after reap")
	}
}

func TestBroadcasterRegistry_ResubscribeAfterReap(t *testing.T) {
	db := setupTestDB(t)
	cfg := testConfig()

	r := newBroadcasterRegistry(db, cfg)
	defer r.stopAll()

	ch1, _ := r.subscribe("chat", -1)
	r.unsubscribe("chat", ch1)

	// Rows inserted while nobody listens must not replay to the next client.
	insertMessage(t, db, "chat", "<div>while-idle</div>")

	ch2, catchup := r.subscribe("chat", -1)
	defer r.unsubscribe("chat", ch2)

	if len(catchup) != 0 {
		t.Fatalf("expected no catch-up after reap, got %d messages", len(catchup))
	}

	insertMessage(t, db, "chat", "<div>live</div>")
	select {
	case msgs := <-ch2:
		if len(msgs) != 1 || msgs[0].html != "<div>live</div>" {
			t.Fatalf("unexpected live messages after resubscribe: %v", msgs)
		}
	case <-time.After(time.Second):
		t.Fatal("no live delivery after resubscribe")
	}
}

func TestBroadcasterRegistry_JoinLeaveRace(t *testing.T) {
	db := setupTestDB(t)
	cfg := testConfig()

	r := newBroadcasterRegistry(db, cfg)
	defer r.stopAll()

	for range 200 {
		chOld, _ := r.subscribe("chat", -1)

		var chNew chan []message
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			r.unsubscribe("chat", chOld)
		}()
		go func() {
			defer wg.Done()
			chNew, _ = r.subscribe("chat", -1)
		}()
		wg.Wait()

		// The joining client must be registered on the live broadcaster,
		// whichever side won the race.
		r.mu.Lock()
		b, ok := r.broadcasters["chat"]
		r.mu.Unlock()
		if !ok {
			t.Fatal("no broadcaster while a client is subscribed")
		}
		b.mu.Lock()
		_, registered := b.clients[chNew]
		b.mu.Unlock()
		if !registered {
			t.Fatal("joining client lost during reap race")
		}

		r.unsubscribe("chat", chNew)
	}

	r.mu.Lock()
	leaked := len(r.broadcasters)
	r.mu.Unlock()
	if leaked != 0 {
		t.Fatalf("leaked %d broadcasters after all clients left", leaked)
	}
}
