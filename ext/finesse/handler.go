package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

func sseHandler(w http.ResponseWriter, r *http.Request, registry *broadcasterRegistry, shutdown context.Context, cfg Config) {
	// Set CORS headers early so error responses are readable by the browser.
	if origin := matchOrigin(r.Header.Get("Origin"), cfg.AllowOrigins); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}

	token := r.URL.Query().Get("signed_stream")
	if token == "" {
		http.Error(w, "signed_stream parameter required", http.StatusBadRequest)
		return
	}

	channel, valid := verifySignedStream(token, cfg.SigningKey)
	if !valid {
		http.Error(w, "invalid signed stream", http.StatusForbidden)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Fresh connection: pass -1 so subscribe() uses the broadcaster's current
	// latestID under the lock -- eliminates the data race from reading latestID
	// outside the lock.
	// Reconnection: browser sends Last-Event-ID; replay only missed events.
	lastID := int64(-1)
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			lastID = id
		}
	}

	ch, catchup := registry.subscribe(channel, lastID)
	defer registry.unsubscribe(channel, ch)

	log.Printf("client connected  channel=%.40s last_id=%d", channel, lastID)

	// Send any messages missed since lastID before streaming live.
	for _, m := range catchup {
		writeSSEEvent(w, m.id, m.html)
	}
	flusher.Flush()

	for {
		select {
		case <-shutdown.Done():
			return
		case <-r.Context().Done():
			log.Printf("client disconnected channel=%.40s", channel)
			return
		case msgs := <-ch:
			for _, m := range msgs {
				writeSSEEvent(w, m.id, m.html)
			}
			flusher.Flush()
		}
	}
}

// matchOrigin returns the origin if it matches the allowlist, or "" if not.
func matchOrigin(origin string, allowed []string) string {
	for _, a := range allowed {
		if a == "*" || a == origin {
			return a
		}
	}
	return ""
}

// verifySignedStream verifies a signed stream token in ActiveSupport::MessageVerifier
// format and returns the channel name.
// Token format: base64strict(json(channel))--hex(hmac_sha256(base64_data, key))
// The HMAC is computed over the base64 data string, matching Rails' MessageVerifier.
func verifySignedStream(token string, key []byte) (string, bool) {
	parts := strings.SplitN(token, "--", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}

	data := parts[0]

	sig, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", false
	}

	// MessageVerifier HMACs the base64-encoded data string, not the decoded bytes.
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", false
	}

	// Decode the data to extract the channel name.
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", false
	}

	// Turbo's MessageVerifier uses JSON serializer, so the decoded data is a JSON string.
	var channel string
	if err := json.Unmarshal(decoded, &channel); err != nil {
		return "", false
	}

	return channel, true
}

// extractTurboHTML pulls the Turbo stream HTML out of the SolidCable payload.
// SolidCable stores the payload as a plain JSON string: "<turbo-stream ...>"
// Fall back to trying a {"data":"..."} object in case the format ever changes.
func extractTurboHTML(payload []byte) (string, error) {
	// Fast path: plain JSON string (the common case).
	var html string
	if err := json.Unmarshal(payload, &html); err == nil {
		return html, nil
	}

	// Slow path: JSON object with a "data" or "message" key.
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return "", fmt.Errorf("unmarshal: %w", err)
	}
	for _, key := range []string{"data", "message"} {
		if raw, ok := msg[key]; ok {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return "", fmt.Errorf("unmarshal %q value: %w", key, err)
			}
			return s, nil
		}
	}
	return "", fmt.Errorf("unrecognised payload shape")
}

// writeSSEEvent writes one SSE event. Multi-line data is handled per spec:
// each newline becomes a new "data:" line.
func writeSSEEvent(w http.ResponseWriter, id int64, data string) {
	fmt.Fprintf(w, "id: %d\n", id)
	for _, line := range strings.Split(data, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprintln(w) // blank line terminates the event
}
