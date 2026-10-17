package main

import (
	"database/sql"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"
)

const (
	bufferSize = 200

	// replayLimit caps how many rows one reconnect loads from the table.
	// ponytail: a client further behind than this loses its oldest missed
	// rows. Raise it, or page the replay, if that matters.
	replayLimit = 1000

	// PostgreSQL commit-order handling. Ids come from a sequence at INSERT
	// time but rows become visible at COMMIT, so a row can show up after a
	// higher id was already delivered. An id below the newest one that is
	// missing from the table entirely is either still in flight or rolled
	// back. We remember such ids for gapTTL and re-query them each poll.
	// ponytail: a transaction open longer than gapTTL, or holding an id more
	// than gapWindow below the newest visible one, can still be missed. A
	// per-row xid horizon would close that but lets any idle transaction in
	// the database stall every channel.
	gapTTL    = 10 * time.Second
	gapWindow = 10000
)

// message is a decoded SSE event ready to send to clients.
type message struct {
	id   int64
	html string
}

// broadcaster runs a single poll loop and fans out to all connected clients.
type broadcaster struct {
	db        *sql.DB
	channel   []byte
	pollQuery string
	replayQ   string
	postgres  bool
	pgQuery   string
	stopCh    chan struct{}
	doneCh    chan struct{}
	wakeCh    chan struct{} // notify-driven poll wakeup (postgres)

	mu       sync.Mutex
	clients  map[chan []message]struct{}
	buffer   []message // last bufferSize messages, in delivery order
	latestID int64
	// floor: the buffer holds every row of this channel with id > floor.
	// Reconnects from at or above it replay from memory, below it from the table.
	floor int64

	gaps map[int64]time.Time // postgres only; touched by the poll goroutine alone
}

func newBroadcaster(db *sql.DB, channel string, cfg Config) *broadcaster {
	b := &broadcaster{
		db:      db,
		channel: []byte(channel),
		pollQuery: bind(fmt.Sprintf(
			`SELECT id, payload FROM %s WHERE channel = ? AND id > ? ORDER BY id ASC`,
			cfg.TableName), cfg.Postgres()),
		replayQ: bind(fmt.Sprintf(
			`SELECT id, payload FROM %s WHERE channel = ? AND id > ? AND id <= ? ORDER BY id DESC LIMIT ?`,
			cfg.TableName), cfg.Postgres()),
		postgres: cfg.Postgres(),
		pgQuery: fmt.Sprintf(`
WITH new AS (
  SELECT id, payload FROM %[1]s WHERE channel = $1 AND (id > $2 OR id = ANY($3))
), hi AS (
  SELECT max(id) AS m FROM new WHERE id > $2
)
SELECT id, payload FROM new
UNION ALL
SELECT t.id, NULL::bytea FROM %[1]s t, hi WHERE t.id > GREATEST($2, hi.m - $4) AND t.id < hi.m
ORDER BY id`, cfg.TableName),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
		wakeCh:  make(chan struct{}, 1),
		clients: make(map[chan []message]struct{}),
		gaps:    make(map[int64]time.Time),
	}
	// Seed latestID so fresh connections don't replay history.
	query := bind(fmt.Sprintf("SELECT COALESCE(MAX(id), 0) FROM %s WHERE channel = ?", cfg.TableName), cfg.Postgres())
	db.QueryRow(query, b.channel).Scan(&b.latestID)
	b.floor = b.latestID
	go b.run(cfg)
	return b
}

func (b *broadcaster) run(cfg Config) {
	defer close(b.doneCh)
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-b.stopCh:
			return
		case <-ticker.C:
			b.poll()
		case <-b.wakeCh:
			b.poll()
		}
	}
}

// wake triggers an immediate poll. Non-blocking: a pending wakeup already
// guarantees a poll that will see the new rows.
func (b *broadcaster) wake() {
	select {
	case b.wakeCh <- struct{}{}:
	default:
	}
}

func (b *broadcaster) poll() {
	rows, err := b.query()
	if err != nil {
		log.Printf("broadcaster query error: %v", err)
		return
	}
	defer rows.Close()

	msgs, present := scanMessages(rows)
	if b.postgres {
		b.trackGaps(msgs, present)
	}
	if len(msgs) == 0 {
		return
	}

	b.mu.Lock()
	b.buffer = append(b.buffer, msgs...)
	if drop := len(b.buffer) - bufferSize; drop > 0 {
		for _, m := range b.buffer[:drop] {
			b.floor = max(b.floor, m.id)
		}
		b.buffer = b.buffer[drop:]
	}
	b.latestID = max(b.latestID, msgs[len(msgs)-1].id)
	for ch := range b.clients {
		select {
		case ch <- msgs:
		default: // slow client -- drop; they'll catch up on reconnect via Last-Event-ID
		}
	}
	b.mu.Unlock()
}

func (b *broadcaster) query() (*sql.Rows, error) {
	if !b.postgres {
		return b.db.Query(b.pollQuery, b.channel, b.latestID)
	}
	pending := make([]int64, 0, len(b.gaps))
	for id := range b.gaps {
		pending = append(pending, id)
	}
	return b.db.Query(b.pgQuery, b.channel, b.latestID, pending, int64(gapWindow))
}

// scanMessages decodes rows of (id, payload). Rows with a NULL payload only
// report that an id exists; they come back in the second return value.
func scanMessages(rows *sql.Rows) (msgs []message, present map[int64]bool) {
	present = make(map[int64]bool)
	for rows.Next() {
		var id int64
		var payload []byte
		if err := rows.Scan(&id, &payload); err != nil {
			log.Printf("scan error: %v", err)
			continue
		}
		present[id] = true
		if payload == nil {
			continue
		}
		html, err := extractTurboHTML(payload)
		if err != nil {
			log.Printf("payload parse error (id=%d): %v", id, err)
			continue
		}
		msgs = append(msgs, message{id, html})
	}
	return msgs, present
}

// trackGaps records ids that are missing between the previous latestID and
// the newest row (present lists every id the query saw, any channel), clears
// the ones that just arrived, and expires the ones that never will. Missing
// from the whole table, not just this channel: other channels' rows share the
// sequence and are normal holes.
func (b *broadcaster) trackGaps(msgs []message, present map[int64]bool) {
	now := time.Now()
	for _, m := range msgs {
		delete(b.gaps, m.id)
	}
	if n := len(msgs); n > 0 && msgs[n-1].id > b.latestID {
		hi := msgs[n-1].id
		for id := max(b.latestID, hi-gapWindow) + 1; id < hi; id++ {
			if _, ok := b.gaps[id]; !ok && !present[id] {
				b.gaps[id] = now
			}
		}
	}
	for id, since := range b.gaps {
		if now.Sub(since) > gapTTL {
			delete(b.gaps, id)
		}
	}
}

// subscribe registers a client and returns its channel plus every message
// since lastID. If lastID is -1, uses the current latestID (fresh
// connection). Rows above floor come from the buffer, rows at or below it
// from the table, so a reaped broadcaster, a restart or a long disconnect
// still replays. Everything runs under the lock, so no row can fall between
// catch-up and live delivery, or arrive in both.
func (b *broadcaster) subscribe(lastID int64) (chan []message, []message) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if lastID < 0 {
		lastID = b.latestID
	}

	var catchup []message
	if lastID < b.floor {
		catchup = b.replay(lastID)
	}
	for _, m := range b.buffer {
		if m.id > lastID && m.id > b.floor {
			catchup = append(catchup, m)
		}
	}
	// Late commits (postgres) sit out of order in the buffer.
	slices.SortFunc(catchup, func(x, y message) int { return int(x.id - y.id) })

	ch := make(chan []message, 50)
	b.clients[ch] = struct{}{}
	return ch, catchup
}

// replay loads rows in (lastID, floor] from the table, oldest first, keeping
// the newest replayLimit if there are more. Caller holds b.mu.
func (b *broadcaster) replay(lastID int64) []message {
	rows, err := b.db.Query(b.replayQ, b.channel, lastID, b.floor, replayLimit)
	if err != nil {
		log.Printf("replay query error: %v", err)
		return nil
	}
	defer rows.Close()
	msgs, _ := scanMessages(rows)
	slices.Reverse(msgs) // fetched newest first so the limit drops the oldest
	return msgs
}

// unsubscribe removes a client and reports how many remain.
func (b *broadcaster) unsubscribe(ch chan []message) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.clients, ch)
	return len(b.clients)
}

func (b *broadcaster) stop() {
	close(b.stopCh)
	<-b.doneCh
}

// broadcasterRegistry is a registry of per-channel broadcasters.
type broadcasterRegistry struct {
	mu           sync.Mutex
	broadcasters map[string]*broadcaster
	db           *sql.DB
	cfg          Config
}

func newBroadcasterRegistry(db *sql.DB, cfg Config) *broadcasterRegistry {
	return &broadcasterRegistry{
		broadcasters: make(map[string]*broadcaster),
		db:           db,
		cfg:          cfg,
	}
}

// wake pokes a channel's broadcaster after a pg_notify. No-op when nobody is
// subscribed: the broadcaster seeds from MAX(id) on first subscribe anyway.
func (r *broadcasterRegistry) wake(channel string) {
	r.mu.Lock()
	b := r.broadcasters[channel]
	r.mu.Unlock()
	if b != nil {
		b.wake()
	}
}

// subscribe registers a client on the channel's broadcaster, creating it if
// needed. Get-or-create and reaping both run under the registry lock, so a
// joining client can never land on a stopped broadcaster.
func (r *broadcasterRegistry) subscribe(channel string, lastID int64) (chan []message, []message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.broadcasters[channel]
	if !ok {
		b = newBroadcaster(r.db, channel, r.cfg)
		r.broadcasters[channel] = b
	}
	return b.subscribe(lastID)
}

// unsubscribe removes a client. When the last one leaves, the broadcaster is
// reaped: removed from the registry and its poll goroutine stopped. The next
// subscriber gets a fresh broadcaster that re-seeds latestID from MAX(id).
// Fresh connections replay nothing; reconnects replay from the table.
func (r *broadcasterRegistry) unsubscribe(channel string, ch chan []message) {
	r.mu.Lock()
	b, ok := r.broadcasters[channel]
	if !ok {
		// Already reaped by stopAll during shutdown.
		r.mu.Unlock()
		return
	}
	if b.unsubscribe(ch) > 0 {
		r.mu.Unlock()
		return
	}
	delete(r.broadcasters, channel)
	r.mu.Unlock()
	// Stop outside the lock: b is unreachable now, and stop() waits up to
	// one poll interval for the goroutine to exit.
	b.stop()
}

func (r *broadcasterRegistry) stopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.broadcasters {
		b.stop()
	}
	// Clear the map so a handler's deferred unsubscribe, racing shutdown,
	// cannot stop a broadcaster twice.
	r.broadcasters = make(map[string]*broadcaster)
}
