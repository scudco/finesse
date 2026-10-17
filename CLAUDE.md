# Finesse

SSE server gem that replaces ActionCable's WebSocket transport. A Ruby gem wraps a Go binary that watches SolidCable's database (SQLite or PostgreSQL 14+) and streams Turbo updates to browsers via Server-Sent Events.

## Architecture

Two languages, clear boundary:

- **Ruby side** (`lib/`, `exe/finesse`) — gem packaging, platform detection, signing key derivation from Rails secrets. The exe wrapper is the entrypoint: it resolves the db path (SQLite) or DSN (Postgres), derives the HMAC signing key via `rails runner`, then `exec`s the Go binary.
- **Go side** (`ext/finesse/`) — the actual SSE server. Watches SolidCable's table (SQLite polling, or Postgres LISTEN/NOTIFY with a polling safety net), verifies signed stream tokens (HMAC-SHA256), fans out to connected browsers. Zero Ruby dependencies at runtime.

The signing key flow is the most complex part: Rails encrypts `secret_key_base` in credentials, derives a signing key via PBKDF2, and Turbo uses it to sign channel names. The Ruby wrapper extracts this derived key and passes it as a hex env var (`FINESSE_SIGNING_KEY`) to the Go binary, which uses it to verify tokens on every `/events` request.

## Key Files

```
exe/finesse                    Ruby wrapper — resolves db path + signing key, execs Go binary
lib/finesse.rb                 Finesse.signing_key — hex-encodes Turbo's verifier key
lib/finesse/platforms.rb       Platform detection, binary path resolution, database.yml parsing
lib/puma/plugin/finesse.rb     Puma plugin shim — makes `plugin :finesse` resolvable in puma.rb
lib/finesse/puma_plugin.rb     Puma plugin logic — spawns/monitors/stops the Go binary from inside Puma
ext/finesse/main.go            Server entrypoint — HTTP routing, version, Postgres NOTIFY listener startup, drain-then-stop shutdown
ext/finesse/config.go          CLI flag parsing, env var reading (signing keys), input validation
ext/finesse/store.go           Backend open (SQLite WAL / pgx), ?→$n binding, pg_notify trigger install + LISTEN loop
ext/finesse/handler.go         SSE handler (retry, heartbeats), multi-key HMAC token verification, CORS, Turbo payload extraction
ext/finesse/broadcaster.go     Per-channel polling + notify wakeup, ring buffer (200 msgs), client fan-out
```

## Commands

```sh
bundle exec rake test          # Run Go + Ruby tests
bundle exec rake test:go       # Go tests only
bundle exec rake test:ruby     # Ruby tests only
bundle exec rake build:local   # Compile Go binary for current platform
bundle exec rake build:all     # Cross-compile for all 4 platforms
bundle exec rake release_build # Tests + build all + gem package
```

Always use `bundle exec` — Ruby tests spawn subprocesses that need the gem's lib/ on the load path.

## Test Structure

- `test/exe/finesse_test.rb` — exe wrapper integration tests (db-path/db-url injection, flag passthrough)
- `test/lib/finesse_test.rb` — signing key derivation (stubs Turbo module)
- `test/lib/finesse/platforms_test.rb` — platform detection, binary resolution, database.yml parsing, Postgres DSN building
- `test/lib/finesse/puma_plugin_test.rb` — plugin command/env assembly, origin parsing, skip-on-misconfiguration
- `ext/finesse/handler_test.go` — token verification (multi-key), payload extraction, SSE formatting, CORS, retry/heartbeat/version
- `ext/finesse/main_test.go` — drain: stream stays open, closes with 200/EOF, listener refused, second signal aborts
- `ext/finesse/broadcaster_test.go` — subscriptions, catchup, fan-out, channel isolation, buffer limits, notify wakeup
- `ext/finesse/config_test.go` — required flag/env validation, signing key parsing (subprocess exit-code tests)
- `ext/finesse/store_test.go` — `?`→`$n` placeholder binding
- `ext/finesse/postgres_test.go` — end-to-end LISTEN/NOTIFY against a real PG (skipped unless `FINESSE_TEST_PG_URL` is set)

## Token Format

```
base64strict(json(channel_name))--hex(hmac_sha256(base64_data, derived_key))
```

Compatible with Rails' `ActiveSupport::MessageVerifier`. The HMAC is computed over the base64 string, not decoded bytes.

## Supported Platforms

arm64-darwin, x86_64-darwin, aarch64-linux, x86_64-linux (precompiled Go binaries in `exe/`).
