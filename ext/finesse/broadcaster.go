package main

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"
)

const bufferSize = 200

// message is a decoded SSE event ready to send to clients.
type message struct {
	id   int64
	html string
}

// broadcaster runs a single poll loop and fans out to all connected clients.
type broadcaster struct {
	db        *sql.DB
	channel   []byte
	tableName string
	stopCh    chan struct{}

	mu       sync.Mutex
	clients  map[chan []message]struct{}
	buffer   []message // ring of last bufferSize messages
	latestID int64
}

func newBroadcaster(db *sql.DB, channel string, cfg Config) *broadcaster {
	b := &broadcaster{
		db:        db,
		channel:   []byte(channel),
		tableName: cfg.TableName,
		clients:   make(map[chan []message]struct{}),
		stopCh:    make(chan struct{}),
	}
	// Seed latestID so fresh connections don't replay history.
	query := fmt.Sprintf("SELECT COALESCE(MAX(id), 0) FROM %s WHERE channel = ?", b.tableName)
	db.QueryRow(query, b.channel).Scan(&b.latestID)
	go b.run(cfg)
	return b
}

func (b *broadcaster) run(cfg Config) {
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-b.stopCh:
			return
		case <-ticker.C:
			b.poll()
		}
	}
}

func (b *broadcaster) poll() {
	query := fmt.Sprintf(
		`SELECT id, payload FROM %s WHERE channel = ? AND id > ? ORDER BY id ASC`,
		b.tableName,
	)
	rows, err := b.db.Query(query, b.channel, b.latestID)
	if err != nil {
		log.Printf("broadcaster query error: %v", err)
		return
	}
	defer rows.Close()

	var msgs []message
	for rows.Next() {
		var id int64
		var payload []byte
		if err := rows.Scan(&id, &payload); err != nil {
			log.Printf("scan error: %v", err)
			continue
		}
		html, err := extractTurboHTML(payload)
		if err != nil {
			log.Printf("payload parse error (id=%d): %v", id, err)
			continue
		}
		msgs = append(msgs, message{id, html})
	}

	if len(msgs) == 0 {
		return
	}

	b.mu.Lock()
	b.buffer = append(b.buffer, msgs...)
	if len(b.buffer) > bufferSize {
		b.buffer = b.buffer[len(b.buffer)-bufferSize:]
	}
	b.latestID = msgs[len(msgs)-1].id
	for ch := range b.clients {
		select {
		case ch <- msgs:
		default: // slow client -- drop; they'll catch up on reconnect via Last-Event-ID
		}
	}
	b.mu.Unlock()
}

// subscribe registers a client and returns its channel plus any buffered
// messages since lastID. If lastID is -1, uses the current latestID (fresh
// connection). Subscribing under the lock closes the race window between
// catch-up and live delivery.
func (b *broadcaster) subscribe(lastID int64) (chan []message, []message) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if lastID < 0 {
		lastID = b.latestID
	}

	var catchup []message
	for _, m := range b.buffer {
		if m.id > lastID {
			catchup = append(catchup, m)
		}
	}

	ch := make(chan []message, 50)
	b.clients[ch] = struct{}{}
	return ch, catchup
}

func (b *broadcaster) unsubscribe(ch chan []message) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
}

func (b *broadcaster) stop() {
	close(b.stopCh)
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

func (r *broadcasterRegistry) get(channel string) *broadcaster {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.broadcasters[channel]; ok {
		return b
	}
	b := newBroadcaster(r.db, channel, r.cfg)
	r.broadcasters[channel] = b
	return b
}

func (r *broadcasterRegistry) stopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.broadcasters {
		b.stop()
	}
}
