# Finesse

SSE server gem that replaces ActionCable's WebSocket transport. A Ruby gem wraps a Go binary that polls SolidCable's SQLite table and streams Turbo updates to browsers via Server-Sent Events.

## Architecture

Two languages, clear boundary:

- **Ruby side** (`lib/`, `exe/finesse`) — gem packaging, platform detection, signing key derivation from Rails secrets. The exe wrapper is the entrypoint: it resolves the db path, derives the HMAC signing key via `rails runner`, then `exec`s the Go binary.
- **Go side** (`ext/finesse/`) — the actual SSE server. Polls SQLite, verifies signed stream tokens (HMAC-SHA256), fans out to connected browsers. Zero Ruby dependencies at runtime.

The signing key flow is the most complex part: Rails encrypts `secret_key_base` in credentials, derives a signing key via PBKDF2, and Turbo uses it to sign channel names. The Ruby wrapper extracts this derived key and passes it as a hex env var (`FINESSE_SIGNING_KEY`) to the Go binary, which uses it to verify tokens on every `/events` request.

## Key Files

```
exe/finesse                    Ruby wrapper — resolves db path + signing key, execs Go binary
lib/finesse.rb                 Finesse.signing_key — hex-encodes Turbo's verifier key
lib/finesse/platforms.rb       Platform detection, binary path resolution, database.yml parsing
ext/finesse/main.go            Server entrypoint — SQLite WAL setup, HTTP routing, graceful shutdown
ext/finesse/config.go          CLI flag parsing, env var reading, input validation
ext/finesse/handler.go         SSE handler, HMAC token verification, CORS, Turbo payload extraction
ext/finesse/broadcaster.go     Per-channel polling, ring buffer (200 msgs), client fan-out
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

- `test/exe/finesse_test.rb` — exe wrapper integration tests (db-path injection, flag passthrough)
- `test/lib/finesse_test.rb` — signing key derivation (stubs Turbo module)
- `test/lib/finesse/platforms_test.rb` — platform detection, binary resolution, database.yml parsing
- `ext/finesse/handler_test.go` — token verification, payload extraction, SSE formatting, CORS
- `ext/finesse/broadcaster_test.go` — subscriptions, catchup, fan-out, channel isolation, buffer limits
- `ext/finesse/config_test.go` — required flag/env validation (subprocess exit-code tests)

## Token Format

```
base64strict(json(channel_name))--hex(hmac_sha256(base64_data, derived_key))
```

Compatible with Rails' `ActiveSupport::MessageVerifier`. The HMAC is computed over the base64 string, not decoded bytes.

## Supported Platforms

arm64-darwin, x86_64-darwin, aarch64-linux, x86_64-linux (precompiled Go binaries in `exe/`).
