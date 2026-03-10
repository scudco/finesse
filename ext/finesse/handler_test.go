package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
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

	channel, ok := verifySignedStream(token, testKey)
	if !ok {
		t.Fatal("expected valid token")
	}
	if channel != "chat" {
		t.Fatalf("expected channel %q, got %q", "chat", channel)
	}
}

func TestVerifySignedStream_WrongKey(t *testing.T) {
	token := signStream("chat", testKey)
	_, ok := verifySignedStream(token, []byte("wrong-key"))
	if ok {
		t.Fatal("expected invalid token with wrong key")
	}
}

func TestVerifySignedStream_TamperedData(t *testing.T) {
	token := signStream("chat", testKey)
	// Replace first character of the base64 data.
	tampered := "X" + token[1:]
	_, ok := verifySignedStream(tampered, testKey)
	if ok {
		t.Fatal("expected invalid token with tampered data")
	}
}

func TestVerifySignedStream_EmptyToken(t *testing.T) {
	cases := []string{"", "--", "abc", "abc--", "--abc"}
	for _, tc := range cases {
		_, ok := verifySignedStream(tc, testKey)
		if ok {
			t.Fatalf("expected invalid for token %q", tc)
		}
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
	writeSSEEvent(w, 42, "<div>hello</div>")
	buf.Write(w.Body.Bytes())

	expected := "id: 42\ndata: <div>hello</div>\n\n"
	if buf.String() != expected {
		t.Fatalf("expected %q, got %q", expected, buf.String())
	}
}

func TestWriteSSEEvent_Multiline(t *testing.T) {
	w := httptest.NewRecorder()
	writeSSEEvent(w, 1, "line1\nline2\nline3")

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
	cfg := Config{SigningKey: testKey, AllowOrigins: []string{"*"}}
	sseHandler(w, req, nil, req.Context(), cfg)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSSEHandler_InvalidSignedStream(t *testing.T) {
	req := httptest.NewRequest("GET", "/events?signed_stream=bogus--token", nil)
	w := httptest.NewRecorder()
	cfg := Config{SigningKey: testKey, AllowOrigins: []string{"*"}}
	sseHandler(w, req, nil, req.Context(), cfg)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}
