# Realtime Horizon: What Finesse Should Support Next

Research date: 2026-10-17. Scope: mature-but-low-adoption realtime features
that fit Finesse's SSE architecture. Everything below is ranked by value per
unit of effort, with sources for each claim.

## Baseline: what Finesse does today

From `ext/finesse/handler.go`, `main.go`, and `broadcaster.go`:

- Serves plain `text/event-stream` over HTTP/1.1 (`http.Server` with no TLS,
  so Go's automatic HTTP/2 never engages; TLS termination is delegated to
  kamal-proxy or nginx).
- Sets `id:` on every event and honors `Last-Event-ID` on reconnect, replaying
  from a 200-message in-memory ring buffer per channel.
- Sends **no** comment heartbeats, **no** `retry:` directive, and no named
  `event:` types. The event loop only writes real messages.
- On reconnect, replay works only if the broadcaster is still alive. When the
  last client leaves a channel the broadcaster is reaped and its buffer is
  destroyed; a later reconnect silently skips messages inserted in the gap
  (`broadcaster.go` re-seeds from `MAX(id)`).

---

## Ranked recommendations

### 1. Comment heartbeats (keepalive) — S

**What:** Periodically write a comment line (`: ping\n\n`) on idle
connections. Comments are part of the SSE wire grammar
(`comment = colon *any-char end-of-line`) and are ignored by EventSource
parsers.
[HTML spec, event stream interpretation](https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation)

**Maturity:** Spec-stable since the beginning of SSE; every proxy and client
passes comment lines through. Zero client work.

**What it buys Finesse:** Dead-connection detection and NAT/proxy keepalive.
Today the event loop only wakes on messages or context cancellation, so a
silently dropped TCP connection (mobile network switch, idle NAT table) is
never noticed, and the README's nginx example compensates with
`proxy_read_timeout 86400s`, which pins dead sockets for a day. A 15–25 s
heartbeat lets that timeout drop to ~60 s and lets failed writes surface
within one interval. Implementation is a `time.Ticker` in the existing
`select` in `sseHandler`.

### 2. `retry:` directive — S

**What:** Send `retry: <ms>` once at stream start. The spec defines it: if
the field value is all ASCII digits, the client sets its reconnection time to
that integer.
[HTML spec, process the field](https://html.spec.whatwg.org/multipage/server-sent-events.html#processField)

**Maturity:** Spec-stable, supported by every EventSource implementation.

**What it buys Finesse:** Control over reconnect storms. Default browser
retry is around 3 s; during a Finesse restart with hundreds of tabs, every
client hammers the port simultaneously. A `retry:` plus jitter guidance lets
operators tune this without touching client JS. One `fmt.Fprintf` after the
headers flush.

### 3. 204 No Content on drain — S

**What:** During graceful shutdown, respond to new `/events` requests with
`204 No Content`. The spec is explicit: "Clients will reconnect if the
connection is closed; a client can be told to stop reconnecting using the
HTTP 204 No Content response code."
[HTML spec, SSE introduction](https://html.spec.whatwg.org/multipage/server-sent-events.html#server-sent-events-intro)

**Maturity:** Spec-stable, universal client behavior.

**What it buys Finesse:** Clean deploys. Today `SIGTERM` cancels the shared
context and drops every stream; all clients immediately reconnect and race
the new process (or fail against the closing kamal-proxy routing window).
Returning 204 during the drain window stops reconnection cleanly until the
new process is up. Pairs with kamal-proxy's gapless deployment model.
[Kamal proxy docs](https://kamal-deploy.org/docs/configuration/proxy/)

### 4. HTTP/2 on the client-facing hop — S to M (mostly deployment)

**What:** Serve browsers over HTTP/2 so SSE streams multiplex onto one TCP
connection per origin. MDN's EventSource reference carries the warning:
when not used over HTTP/2, SSE is limited to ~6 concurrent open connections
**per browser**, and extra tabs hang.
[MDN EventSource](https://developer.mozilla.org/en-US/docs/Web/API/EventSource),
[Chromium issue 40329530](https://issues.chromium.org/40329530)

**Maturity:** HTTP/2 is RFC 9113 (2022) and universal. Go's `net/http` has
served HTTP/2 transparently over HTTPS since Go 1.6.
[pkg.go.dev/net/http](https://pkg.go.dev/net/http)

**What it buys Finesse:** Removes the one hard scaling wall SSE has. A user
with 8 tabs of the same app today stalls on tab 7 when hitting Finesse
directly. Note Finesse itself needs no code change in the common topology:
kamal-proxy terminates TLS, and since it is a Go server it speaks HTTP/2 to
browsers automatically
([kamal-proxy repo](https://github.com/basecamp/kamal-proxy),
[INFERENCE] from Go's transparent h2); the kamal-proxy-to-Finesse hop stays
HTTP/1.1, which is fine because it is one connection per stream regardless.
The work is (a) documenting this in the deployment guide, and (b) optionally
adding `ListenAndServeTLS` for direct-exposure setups. If a proxy ever needs
h2 upstream, Go does not do cleartext h2 out of the box, but
[`golang.org/x/net/http2/h2c`](https://pkg.go.dev/golang.org/x/net/http2/h2c)
is the standard shim (one middleware line).

### 5. Replay from the SQLite table, not just the ring buffer — M

**What:** On reconnect with `Last-Event-ID`, run
`SELECT id, payload FROM solid_cable_messages WHERE channel=? AND id > ?`
before subscribing, instead of relying solely on the 200-message ring buffer.

**Maturity:** No new technology; it uses the table Finesse already polls.
This upgrades an existing, spec-defined contract (`Last-Event-ID` /
`Last-Event-ID` request header) rather than inventing one.
[MDN Using server-sent events](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events/Using_server-sent_events)

**What it buys Finesse:** Closes a real correctness gap in the resume story.
Today replay is lost in two cases: more than 200 messages arrive during a
disconnect, or the broadcaster was reaped while zero clients were connected
(the common case for a single-user app where the one tab closes and reopens
minutes later). The rows are still in the table; the query is already
indexed by the polling path. The ring buffer stays as the fast path.

### 6. bfcache-safe client reconnection — S (client snippet + docs)

**What:** Close the EventSource on `pagehide`, reopen on `pageshow`
(`event.persisted === true`), letting `Last-Event-ID` resume do the rest.
Open long-lived fetches (which EventSource is) make pages ineligible for the
back/forward cache.
[Chromium bfcache docs](https://chromium.googlesource.com/chromium/src/+/HEAD/docs/bfcache.md),
[web.dev bfcache](https://web.dev/articles/bfcache),
[MDN visibilitychange](https://developer.mozilla.org/en-US/docs/Web/API/Document/visibilitychange_event)

**Maturity:** `pagehide`/`pageshow`/`persisted` are Baseline across all
browsers. Chrome's `freeze`/`resume` events from the Page Lifecycle API are
Chrome-only and non-standard; do not build on them.

**What it buys Finesse:** Instant back/forward navigation for apps using
`turbo-stream-source`, plus fewer zombie connections: today a
bfcache-blocked page holds its SSE stream open indefinitely in the
background. This is a small JS patch to the `<turbo-stream-source>` element
(or its Stimulus controller) plus a docs note; the server already supports
the resume half.

### 7. Cross-tab connection sharing — M

**What:** One tab (or a `SharedWorker`) owns the EventSource and fans
messages out to other tabs over `BroadcastChannel`. `Web Locks` provides
leader election if a SharedWorker is undesirable.

**Maturity:** SharedWorker is at 93.5% global support including Safari 16+
([caniuse](https://caniuse.com/sharedworkers)); BroadcastChannel and
[Web Locks](https://developer.mozilla.org/en-US/docs/Web/API/Web_Locks_API)
are Baseline. EventSource itself is available inside workers.
[MDN EventSource](https://developer.mozilla.org/en-US/docs/Web/API/EventSource)

**What it buys Finesse:** Divides per-user connection count by tab count.
For the app whose users keep 5 tabs open, server-side connections drop 5x
without any server change. This matters more than per-connection efficiency
because connection count is what drives broadcaster fan-out cost. The lazy
version is leader-election-in-tab with Web Locks plus BroadcastChannel,
which avoids SharedWorker lifecycle quirks; it is roughly a page of
client JS.

### 8. Named `event:` types for channel multiplexing — M, defer until multi-stream pages are common

**What:** Emit `event: <channel>` alongside `data:`, letting one EventSource
carry several channels; clients subscribe per type with
`addEventListener(type, …)`.
[MDN event stream format](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events/Using_server-sent_events#event_stream_format)

**Maturity:** Spec-stable, universal.

**What it buys Finesse:** One connection per tab instead of one per
`turbo-stream-source` element. Real but currently redundant with items 4 and
7, which solve the same connection-count problem with less client surgery,
and `turbo-stream-source` creates one EventSource per element today, so this
requires a custom client. Revisit if item 7 lands and connection count is
still the bottleneck.

### 9. HTTP/3 / QUIC — L, defer

**What:** Serve `/events` over HTTP/3. quic-go is production-ready, pure Go,
implements RFC 9114 plus QPACK and RFC 9218 priorities, and exposes a
drop-in `http3.Server`.
[quic-go README](https://github.com/quic-go/quic-go),
[pkg.go.dev http3](https://pkg.go.dev/github.com/quic-go/quic-go/http3)

**Maturity:** HTTP/3 is standardized and EventSource works over it
transparently (it is ordinary HTTP to the client).

**What it buys Finesse:** Little today. HTTP/3's win over HTTP/2 is removing
TCP head-of-line blocking *between* streams; an SSE connection is a single
long stream, so HOL barely applies. It also requires UDP 443 open and
Alt-Svc advertisement, and kamal-proxy does not terminate QUIC, so it only
helps direct-exposure deployments. Worth a spike only if mobile-loss
networks become a reported problem.

### 10. Long-poll fallback for buffering middleboxes — M, defer

**What:** A `GET /events/poll?after=<id>` endpoint returning buffered
messages and closing, for clients behind corporate proxies/AV scanners that
buffer or kill long-lived responses.

**Maturity:** Long polling is ancient and works everywhere. Detection is the
hard part (EventSource just fires `error`).

**What it buys Finesse:** Insurance against hostile middleboxes. Finesse
already sets `X-Accel-Buffering: no` and the documented proxies (nginx,
kamal-proxy, which auto-detects `text/event-stream`) handle SSE correctly.
Add only when a real deployment report shows buffering in the wild.

---

## Killed: immature or wrong-fit

- **WebSocketStream.** Chromium-only (72.7% global, no Firefox/Safari), still
  WICG. It also buys nothing over SSE for one-way server push.
  [caniuse](https://caniuse.com/mdn-api_websocketstream)
- **WebTransport.** Now at ~91.6% global support including Safari, but it
  requires an HTTP/3 server and a new session/datagram stack, and its
  advantages (unreliable datagrams, client push) are irrelevant to
  broadcast-one-way HTML fragments. Revisit only if Finesse ever needs
  client-to-server realtime.
  [caniuse](https://caniuse.com/webtransport)
- **RFC 9218 stream priorities.** Go's `x/net/http2` added support
  ([golang/go#75500](https://github.com/golang/go/issues/75500),
  [RFC 9218](https://www.rfc-editor.org/rfc/rfc9218.html)), but priority
  scheduling matters when many resources compete on one connection. Finesse
  serves one resource per connection. Nothing to schedule.
- **103 Early Hints (RFC 8297).** For preloading render-blocking assets on
  document responses. A streaming endpoint has no head-of-response
  negotiation problem to solve.
- **Brotli/zstd `Content-Encoding` on the stream.** Browser support is now
  broad (zstd: Chrome 123+
  [Chrome for Developers](https://developer.chrome.com/blog/chrome-123-beta),
  Firefox 126+, Safari 18.4+
  [WebKit](https://webkit.org/blog/16574/webkit-features-in-safari-18-4);
  [caniuse zstd](https://caniuse.com/zstd)), and
  [klauspost/compress](https://github.com/klauspost/compress) is mature with
  flush support. Killed on fit, not maturity: Turbo payloads are small HTML
  fragments flushed individually, so per-flush compression saves little and
  adds a CPU cost on every fan-out write. The framing overhead it would
  shrink is already tiny.
- **HTTP/2 server push.** Removed from Chrome in 2022; dead everywhere that
  matters. Not evaluated further.

---

## Suggested order

1. Heartbeats + `retry:` + 204 drain (one PR, all in `handler.go`/`main.go`).
2. Table-backed replay (closes the resume gap).
3. bfcache client snippet + h2 deployment docs.
4. Cross-tab sharing (client JS only).
5. Defer: named-event multiplexing, h3, long-poll fallback.
