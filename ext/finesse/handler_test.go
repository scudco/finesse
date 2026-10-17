package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// signStream produces a valid signed stream token for testing.
func signStream(channel string, key []byte) string {
	data := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%q", channel)))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return data + "--" + hex.EncodeToString(mac.Sum(nil))
}

var testKey = []byte("test-secret-key-for-finesse")

func TestVerifySignedStream(t *testing.T) {
	token := signStream("chat", testKey)

	channel, ok := verifySignedStream(token, [][]byte{testKey})
	if !ok {
		t.Fatal("expected valid token")
	}
	if channel != "chat" {
		t.Fatalf("expected channel %q, got %q", "chat", channel)
	}
}

func TestVerifySignedStream_WrongKey(t *testing.T) {
	token := signStream("chat", testKey)
	_, ok := verifySignedStream(token, [][]byte{[]byte("wrong-key")})
	if ok {
		t.Fatal("expected invalid token with wrong key")
	}
}

func TestVerifySignedStream_TamperedData(t *testing.T) {
	token := signStream("chat", testKey)
	// Replace first character of the base64 data.
	tampered := "X" + token[1:]
	_, ok := verifySignedStream(tampered, [][]byte{testKey})
	if ok {
		t.Fatal("expected invalid token with tampered data")
	}
}

func TestVerifySignedStream_EmptyToken(t *testing.T) {
	cases := []string{"", "--", "abc", "abc--", "--abc"}
	for _, tc := range cases {
		_, ok := verifySignedStream(tc, [][]byte{testKey})
		if ok {
			t.Fatalf("expected invalid for token %q", tc)
		}
	}
}

func TestVerifySignedStream_MultipleKeys(t *testing.T) {
	oldKey, newKey := []byte("old-key"), []byte("new-key")
	keys := [][]byte{newKey, oldKey}

	for _, signer := range [][]byte{oldKey, newKey} {
		if channel, ok := verifySignedStream(signStream("chat", signer), keys); !ok || channel != "chat" {
			t.Fatalf("token signed by %q should verify, got %q ok=%v", signer, channel, ok)
		}
	}
	if _, ok := verifySignedStream(signStream("chat", []byte("retired-key")), keys); ok {
		t.Fatal("token signed by an unknown key must fail")
	}
}

func TestExtractTurboHTML_PlainString(t *testing.T) {
	payload := []byte(`"<turbo-stream action=\"append\"><template>hi</template></turbo-stream>"`)
	html, err := extractTurboHTML(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if html != `<turbo-stream action="append"><template>hi</template></turbo-stream>` {
		t.Fatalf("unexpected html: %q", html)
	}
}

func TestExtractTurboHTML_DataObject(t *testing.T) {
	payload := []byte(`{"data":"<div>hello</div>"}`)
	html, err := extractTurboHTML(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if html != "<div>hello</div>" {
		t.Fatalf("unexpected html: %q", html)
	}
}

func TestExtractTurboHTML_MessageObject(t *testing.T) {
	payload := []byte(`{"message":"<p>msg</p>"}`)
	html, err := extractTurboHTML(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if html != "<p>msg</p>" {
		t.Fatalf("unexpected html: %q", html)
	}
}

func TestExtractTurboHTML_InvalidPayload(t *testing.T) {
	_, err := extractTurboHTML([]byte(`{"unknown":"value"}`))
	if err == nil {
		t.Fatal("expected error for unrecognised payload shape")
	}
}

func TestExtractTurboHTML_InvalidJSON(t *testing.T) {
	_, err := extractTurboHTML([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWriteSSEEvent(t *testing.T) {
	var buf bytes.Buffer
	w := httptest.NewRecorder()
	writeSSEEvent(w, message{id: 42, html: "<div>hello</div>"})
	buf.Write(w.Body.Bytes())

	expected := "id: 42\ndata: <div>hello</div>\n\n"
	if buf.String() != expected {
		t.Fatalf("expected %q, got %q", expected, buf.String())
	}
}

func TestWriteSSEEvent_LateMessageHasNoID(t *testing.T) {
	w := httptest.NewRecorder()
	writeSSEEvent(w, message{id: 5, html: "<div>late</div>", late: true})

	expected := "data: <div>late</div>\n\n"
	if w.Body.String() != expected {
		t.Fatalf("expected %q, got %q", expected, w.Body.String())
	}
}

func TestWriteSSEEvent_Multiline(t *testing.T) {
	w := httptest.NewRecorder()
	writeSSEEvent(w, message{id: 1, html: "line1\nline2\nline3"})

	expected := "id: 1\ndata: line1\ndata: line2\ndata: line3\n\n"
	if w.Body.String() != expected {
		t.Fatalf("expected %q, got %q", expected, w.Body.String())
	}
}

func TestMatchOrigin(t *testing.T) {
	allowed := []string{"http://localhost:3000", "https://example.com"}

	tests := []struct {
		origin string
		want   string
	}{
		{"http://localhost:3000", "http://localhost:3000"},
		{"https://example.com", "https://example.com"},
		{"https://evil.com", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := matchOrigin(tt.origin, allowed)
		if got != tt.want {
			t.Errorf("matchOrigin(%q) = %q, want %q", tt.origin, got, tt.want)
		}
	}
}

func TestMatchOrigin_Wildcard(t *testing.T) {
	got := matchOrigin("https://anything.com", []string{"*"})
	if got != "*" {
		t.Fatalf("expected wildcard match, got %q", got)
	}
}

func TestSSEHandler_MissingSignedStream(t *testing.T) {
	req := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()
	cfg := Config{SigningKeys: [][]byte{testKey}, AllowOrigins: []string{"*"}}
	sseHandler(w, req, nil, req.Context(), cfg)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSSEHandler_InvalidSignedStream(t *testing.T) {
	req := httptest.NewRequest("GET", "/events?signed_stream=bogus--token", nil)
	w := httptest.NewRecorder()
	cfg := Config{SigningKeys: [][]byte{testKey}, AllowOrigins: []string{"*"}}
	sseHandler(w, req, nil, req.Context(), cfg)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

// openStream serves sseHandler over a real HTTP server and returns the
// response for a valid /events request on channel "chat".
func openStream(t *testing.T, cfg Config) *http.Response {
	t.Helper()
	cfg.SigningKeys = [][]byte{testKey}
	cfg.AllowOrigins = []string{"*"}
	cfg.TableName = "solid_cable_messages"
	cfg.PollInterval = 5 * time.Millisecond

	registry := newBroadcasterRegistry(setupTestDB(t), cfg)
	t.Cleanup(registry.stopAll)
	shutdown, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseHandler(w, r, registry, shutdown, cfg)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(cancel) // runs first: end the stream so srv.Close does not block

	resp, err := http.Get(srv.URL + "/events?signed_stream=" + signStream("chat", testKey))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestSSEHandler_RetryIsFirstFrame(t *testing.T) {
	resp := openStream(t, Config{RetryMS: 1500})
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if line != "retry: 1500\n" {
		t.Fatalf("first line = %q, want retry directive", line)
	}
	if v := resp.Header.Get("X-Finesse-Version"); v != version {
		t.Fatalf("X-Finesse-Version = %q, want %q", v, version)
	}
}

func TestSSEHandler_HeartbeatComment(t *testing.T) {
	resp := openStream(t, Config{Heartbeat: 20 * time.Millisecond})
	r := bufio.NewReader(resp.Body)
	for i := 0; i < 2; i++ {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if line != ": ping\n" {
			t.Fatalf("ping %d = %q, want a comment line", i, line)
		}
		if blank, _ := r.ReadString('\n'); blank != "\n" {
			t.Fatalf("ping %d not terminated by a blank line: %q", i, blank)
		}
	}
}

func TestSSEHandler_NoHeartbeatWhenDisabled(t *testing.T) {
	resp := openStream(t, Config{})
	got := make(chan string, 1)
	go func() {
		b := make([]byte, 1)
		n, _ := resp.Body.Read(b)
		got <- string(b[:n])
	}()
	select {
	case s := <-got:
		t.Fatalf("unexpected bytes with heartbeat and retry off: %q", s)
	case <-time.After(100 * time.Millisecond):
	}
}
