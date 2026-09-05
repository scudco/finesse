<p align="center">
  <img src="finesse-logo.png" alt="Finesse" width="200">
</p>

# Finesse

Finesse replaces ActionCable's WebSocket transport with a lightweight Go binary that polls SolidCable's SQLite table and streams Turbo updates to browsers via Server-Sent Events (SSE). No WebSocket infrastructure needed — just a single binary alongside your Rails app.

In a fan-out benchmark against ActionCable on Puma, Finesse halved delivery latency at everyday load and kept delivering every message at a load that saturated Puma ([numbers below](#why-finesse)).

## Why Finesse

ActionCable holds every WebSocket open inside your Rails server, and each message rides through the Ruby pub/sub stack. Finesse moves that fan-out work into a small Go binary that speaks plain HTTP.

### Benchmark

Rails 8 in production mode, Puma defaults (single worker, 3 threads). Both servers read the same SolidCable SQLite table, polling at 10 ms. One broadcaster stamps each message with its send time; latency is broadcast to browser receipt. Apple M1.

**Steady load** (10 broadcasts/s, 20 s windows, zero loss on both sides):

| Clients | Finesse p50 / p99 | ActionCable p50 / p99 | Finesse CPU / RSS | ActionCable CPU / RSS |
|--------:|------------------:|----------------------:|------------------:|----------------------:|
| 1       | 7 ms / 12 ms      | 9 ms / 21 ms          | 4% / 21 MB        | 13% / 163 MB          |
| 50      | 7 ms / 14 ms      | 11 ms / 28 ms         | 5% / 25 MB        | 17% / 167 MB          |
| 200     | 10 ms / 19 ms     | 19 ms / 35 ms         | 7% / 31 MB        | 24% / 172 MB          |
| 500     | 14 ms / 30 ms     | 24 ms / 42 ms         | 11% / 41 MB       | 29% / 180 MB          |

**Stress** (100 broadcasts/s to 500 clients, about 50,000 deliveries/s):

| | Finesse | ActionCable (Puma) |
|---|---:|---:|
| p50 latency | 10 ms | 5,575 ms |
| p99 latency | 16 ms | 10,206 ms |
| delivered | 100% (759k msgs) | 93.1% (428k msgs) |
| CPU / RSS | 51% / 49 MB | 97% / 325 MB |

```
p50 latency at ~50,000 deliveries/s

Finesse      ▏ 10 ms
ActionCable  ████████████████████████████████████████ 5,575 ms
```

At steady load both are lossless; Finesse halves latency and runs on a fraction of ActionCable's CPU and memory. Past Puma's ceiling, latency climbs into seconds and messages drop, while Finesse stays under 20 ms at half a core.

<details>
<summary>Methodology</summary>

- One `rails runner` loop broadcasts via `Turbo::StreamsChannel.broadcast_stream_to`, embedding an epoch-ms timestamp in each payload. Both servers poll the same `production_cable.sqlite3`.
- SolidCable's `polling_interval` was set to 0.01 s to match Finesse's default, so the comparison measures transport, not poll frequency.
- Clients: a Bun harness opening N SSE readers against Finesse or N real ActionCable WebSocket subscribers against Puma, all on one channel. Latency recorded per delivery over a 20 s window after all clients subscribed; CPU and RSS sampled from `ps` once per second.
- Caveats: clients and servers shared one machine, everything ran on localhost without TLS, and Puma used its default single worker (`WEB_CONCURRENCY` would raise its ceiling at the cost of more memory).

</details>

### Simpler on the wire

```
ActionCable                               Finesse

Browser ⇄ WS upgrade ⇄ Puma               Browser ──GET /events──> Go binary
  JSON protocol: subscribe,                 one HTTP response that
  confirm, ping/pong                        never ends (SSE frames)
  missed messages on reconnect:             reconnect + catch-up built into
  gone                                      EventSource (Last-Event-ID)
```

The server's whole job is to poll one SQLite table and write text frames to every connection holding a valid signed token. You can watch a live stream with `curl`.

### The tradeoff: deployment

ActionCable costs nothing to deploy because it already lives inside Puma. Finesse is a second process with its own port and proxy rule, and it needs the signing key handed across the Ruby/Go boundary. In development that is one Procfile line; in production it is one more service to run and monitor (see [Production Deployment](#production-deployment)). If your app broadcasts to a handful of tabs, ActionCable's zero-deploy story wins on simplicity of operations. Finesse wins on simplicity of runtime and on headroom.

## Prerequisites

Finesse currently supports **SolidCable with SQLite** only. Your Rails app needs:

- **[SolidCable](https://github.com/rails/solid_cable)** as your ActionCable adapter, with a `cable:` database configured in `config/database.yml`
- **[turbo-rails](https://github.com/hotwired/turbo-rails)** (>= 2.0) — Finesse uses Turbo's signed stream tokens for authentication

PostgreSQL and other adapters are on the [roadmap](#roadmap).

## Quick Start

1. Add to your Gemfile:
   ```ruby
   gem "finesse"
   ```

2. Install:
   ```sh
   bundle install
   ```

3. Add to your `Procfile.dev`:
   ```
   sse: bundle exec finesse --allow-origin http://localhost:3000
   ```
   For multiple origins (e.g., accessing from another device on your network), repeat the flag:
   ```
   sse: bundle exec finesse --allow-origin http://localhost:3000 --allow-origin http://192.168.1.10:3000
   ```

4. Start your app:
   ```sh
   bin/dev
   ```

## How It Works

```
Browser <turbo-stream-source> ──GET /events?signed_stream=…──> Finesse (Go binary, :4000)
                                                                     │
Rails app ──broadcast_append_to──> SolidCable ──INSERT──> SQLite ←──POLL─┘
```

Finesse polls the `solid_cable_messages` table for new rows and streams them as SSE events to connected browsers. It uses SQLite WAL mode for concurrent read access alongside Rails writes.

Key design choices:
- **Polling over triggers** — simple, no SQLite extensions required, 10ms default interval
- **Per-channel fan-out** — each channel gets its own broadcaster with a ring buffer for reconnection catch-up
- **Pure Go SQLite** — uses `modernc.org/sqlite` (no CGO), enabling easy cross-compilation

## CLI Options

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `4000` | HTTP listen port |
| `--db-path` | auto from `config/database.yml` | Path to SolidCable SQLite database |
| `--table-name` | `solid_cable_messages` | SolidCable messages table name |
| `--poll-interval` | `10` | Database poll interval (integer, milliseconds) |
| `--allow-origin` | *(required)* | `Access-Control-Allow-Origin` header value |

## Environment Variables

| Variable | Description |
|----------|-------------|
| `FINESSE_SIGNING_KEY` | Hex-encoded HMAC key for signed stream verification (auto-derived from Rails when using `bundle exec finesse`) |
| `BINDING` | Bind address (default: `127.0.0.1`) |

## Endpoints

| Path | Description |
|------|-------------|
| `GET /up` | Health check (returns 200) |
| `GET /events?signed_stream=<token>` | SSE stream for the given signed stream |

The SSE endpoint supports the `Last-Event-ID` header for automatic reconnection catch-up. Stream tokens use the same `ActiveSupport::MessageVerifier` format as Turbo's signed streams.

## Development

### Compiling from source

Requires Go 1.24+.

```sh
bundle exec rake build:local   # Build for current platform
bundle exec rake build:all     # Cross-compile for all platforms
```

### Running tests

All build and test commands should be run via `bundle exec`:

```sh
bundle exec rake test          # Run Go + Ruby tests
bundle exec rake test:go       # Go tests only
bundle exec rake test:ruby     # Ruby tests only
```

### Running locally

```sh
./exe/arm64-darwin/finesse --port 4000 --db-path ../your-app/storage/development_cable.sqlite3
```

## Security

Finesse verifies every SSE connection using Turbo's signed stream tokens (HMAC-SHA256). CORS is restricted to the origins you specify with `--allow-origin`, and the server binds to `127.0.0.1` by default.

### Signing Key Flow

**TL;DR:** Finesse needs the same HMAC key that Turbo uses to sign stream names. The Ruby wrapper extracts it from Rails automatically — zero config in development.

The problem: Finesse is a standalone Go binary, but it needs to verify tokens that Rails signs. Rails derives its signing key from `SECRET_KEY_BASE` via PBKDF2, and that secret lives in encrypted credentials (`config/credentials.yml.enc`) which require Ruby's `Marshal` deserializer to read. Reimplementing that in Go would be fragile and version-dependent.

The solution: let Ruby do the Ruby parts, then hand the result to Go.

```
        Rails Boot                           Finesse Boot
        ──────────                           ────────────

config/credentials.yml.enc           exe/finesse (Ruby wrapper)
           │                                    │
           ▼                                    ▼
      SECRET_KEY_BASE                rails runner "Finesse.signing_key"
           │                                    │
           ▼                                    ▼
      PBKDF2-HMAC-SHA256             Turbo.signed_stream_verifier_key
      salt: "turbo/signed_                      │
            stream_verifier_key"                ▼
      iterations: 65,536             hex-encode the derived key
      output: 64 bytes                          │
           │                                    ▼
           ▼                         ENV["FINESSE_SIGNING_KEY"] = hex
      Turbo.signed_stream_                      │
        verifier_key                            ▼
           │                         exec(go_binary)
           ▼                           ├── reads env, decodes hex
      turbo_stream_from @chat          └── verifies HMAC on every
      signs channel name ─────────▶  /events?signed_stream=<token>
```

**Token format** (Rails `ActiveSupport::MessageVerifier`):
```
base64strict(json(channel_name))--hex(hmac_sha256(base64_data, derived_key))
```

The Go binary splits the token on `--`, recomputes the HMAC over the base64 data, and compares using constant-time `hmac.Equal`. If it matches, the channel name is trusted.

**In development**, this is fully automatic — Rails writes `secret_key_base` to `tmp/local_secret.txt` and the wrapper reads it. **In production**, either let the wrapper derive the key at boot (slower — spawns a `rails runner`), or set `FINESSE_SIGNING_KEY` directly as a hex-encoded env var to skip the Rails boot entirely.

## Production Deployment

Finesse binds to `127.0.0.1` by default and should **not** be exposed directly on port 80/443. Run it alongside your app server (Puma, etc.) on the same host and proxy to it from your web server. For example, with Nginx:

```nginx
upstream finesse {
  server 127.0.0.1:4000;
}

location /finesse/ {
  proxy_pass http://finesse/;
  proxy_set_header Host $host;
  proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

  # SSE requires unbuffered responses
  proxy_buffering off;
  proxy_cache off;
  proxy_read_timeout 86400s;
}
```

### Kamal

If you deploy with [Kamal](https://kamal-deploy.org), add Finesse as a separate server role in `config/deploy.yml`. kamal-proxy automatically detects `Content-Type: text/event-stream` responses and disables buffering — no extra proxy configuration needed.

```yaml
servers:
  web:
    - <your-public-server-ip>
  sse:
    hosts:
      - <your-public-server-ip>
    cmd: bundle exec finesse --allow-origin "https://yourapp.com"
    env:
      clear:
        BINDING: 0.0.0.0
    proxy:
      ssl: true
      host: sse.yourapp.com
      app_port: 4000
      healthcheck:
        path: /up
```

Point a DNS record for `sse.yourapp.com` at your server and kamal-proxy handles TLS and routing.

Set `FINESSE_SIGNING_KEY` as a hex-encoded env var in production to avoid a `rails runner` invocation on every boot. The `--allow-origin` flag should match your production domain.

## Roadmap

- **Rails generator** — `rails g finesse:install` for config, initializer, and binstub
- **Engine/Railtie** — view helpers (`finesse_sse_url`)
- **PostgreSQL support** — `LISTEN/NOTIFY` adapter as alternative to SQLite polling. 8KB limit may not be worth it, but polling in PostgreSQL would still be on the roadmap.

## License

MIT License. See [LICENSE](LICENSE) for details.
