<p align="center">
  <img src="finesse-logo.png" alt="Finesse" width="200">
</p>

# Finesse

Finesse replaces ActionCable's WebSocket transport with a lightweight Go binary that polls SolidCable's SQLite table and streams Turbo updates to browsers via Server-Sent Events (SSE). No WebSocket infrastructure needed — just a single binary alongside your Rails app.

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
   sse: bundle exec finesse
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
| `--poll-interval` | `10` | Database poll interval in milliseconds |
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

Requires Go 1.23+.

```sh
# Build for current platform
cd ext/finesse && go build -o ../../exe/$(ruby -e "puts Gem::Platform.local.cpu + '-' + Gem::Platform.local.os")/finesse .

# Cross-compile for all platforms
rake build:all
```

### Running locally

```sh
./exe/arm64-darwin/finesse --port 4000 --db-path ../your-app/storage/development_cable.sqlite3
```

## Security

Finesse verifies every SSE connection using Turbo's signed stream tokens (HMAC-SHA256). The signing key is automatically derived from your Rails app's `SECRET_KEY_BASE` via `Turbo.signed_stream_verifier_key`. CORS is restricted to the origin you specify with `--allow-origin`, and the server binds to `127.0.0.1` by default.

## Roadmap

- **Rails generator** — `rails g finesse:install` for config, initializer, and binstub
- **Engine/Railtie** — view helpers (`finesse_sse_url`)
- **PostgreSQL support** — `LISTEN/NOTIFY` adapter as alternative to SQLite polling

## License

MIT License. See [LICENSE](LICENSE) for details.
