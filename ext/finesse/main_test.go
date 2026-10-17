package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// startStreamServer serves a handler that holds /events open until streams end.
func startStreamServer(t *testing.T) (srv *http.Server, addr string, endStreams func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "retry: 100\n\n")
		w.(http.Flusher).Flush()
		<-ctx.Done()
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { cancel(); srv.Close() })
	return srv, ln.Addr().String(), cancel
}

func TestDrain_KeepsStreamOpenThenClosesCleanly(t *testing.T) {
	srv, addr, endStreams := startStreamServer(t)

	resp, err := http.Get("http://" + addr + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}

	const timeout = 400 * time.Millisecond
	start := time.Now()
	drained := make(chan bool, 1)
	go func() { drained <- drain(srv, endStreams, timeout, make(chan os.Signal)) }()

	// New connections are refused once the listener closes.
	deadline := time.Now().Add(time.Second)
	for {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			break
		}
		c.Close()
		if time.Now().After(deadline) {
			t.Fatal("listener still accepting during drain")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The existing stream is still open partway through the drain window.
	closed := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, resp.Body); closed <- err }()
	select {
	case <-closed:
		t.Fatal("stream closed during drain window")
	case <-time.After(timeout / 4):
	}

	// After the window it ends with a normal close, not an error.
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("stream ended with error %v, want clean EOF", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream never closed")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (EventSource gives up on non-200)", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed < timeout {
		t.Fatalf("stream closed after %s, before the %s drain window", elapsed, timeout)
	}
	if !<-drained {
		t.Fatal("drain reported abort")
	}
}

func TestDrain_SecondSignalAborts(t *testing.T) {
	srv, addr, endStreams := startStreamServer(t)
	resp, err := http.Get("http://" + addr + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	abort := make(chan os.Signal, 1)
	abort <- os.Interrupt
	start := time.Now()
	if drain(srv, endStreams, time.Minute, abort) {
		t.Fatal("drain should report abort on a second signal")
	}
	if time.Since(start) > time.Second {
		t.Fatal("abort did not return promptly")
	}
}
