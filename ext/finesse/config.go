package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// stringSlice implements flag.Value for repeated string flags.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ", ") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// Config holds all runtime configuration parsed from CLI flags.
type Config struct {
	Port         int
	Bind         string
	DBPath       string
	DBURL        string
	TableName    string
	PollInterval time.Duration
	AllowOrigins []string
	SigningKeys  [][]byte
	Heartbeat    time.Duration // 0 disables
	RetryMS      int           // 0 omits the retry: line
	DrainTimeout time.Duration
}

// Postgres reports whether the PostgreSQL backend is configured.
func (c Config) Postgres() bool { return c.DBURL != "" }

// ParseConfig reads CLI flags and returns a Config.
func ParseConfig() Config {
	cfg := Config{}
	var pollMS int
	flag.IntVar(&cfg.Port, "port", 4000, "HTTP listen port")
	flag.StringVar(&cfg.DBPath, "db-path", "storage/development_cable.sqlite3", "path to SolidCable SQLite database")
	flag.StringVar(&cfg.DBURL, "db-url", "", "PostgreSQL connection URL or DSN (selects the PostgreSQL backend)")
	flag.StringVar(&cfg.TableName, "table-name", "solid_cable_messages", "SolidCable messages table name")
	flag.IntVar(&pollMS, "poll-interval", 0, "database poll interval in milliseconds (default 10 for SQLite, 1000 for PostgreSQL)")
	var allowOrigins stringSlice
	flag.Var(&allowOrigins, "allow-origin", "allowed origin (may be repeated, e.g. --allow-origin http://localhost:3000 --allow-origin http://192.168.1.10:3000)")
	flag.DurationVar(&cfg.Heartbeat, "heartbeat", 20*time.Second, "interval between SSE keepalive comments (0 disables)")
	flag.IntVar(&cfg.RetryMS, "retry", 2000, "EventSource reconnect delay in milliseconds sent at stream start (0 omits it)")
	flag.DurationVar(&cfg.DrainTimeout, "drain-timeout", 25*time.Second, "on SIGTERM, how long open streams may live before they are closed")
	flag.Parse()
	cfg.AllowOrigins = allowOrigins
	if pollMS <= 0 {
		if cfg.Postgres() {
			pollMS = 1000 // NOTIFY delivers instantly; polling is only the safety net
		} else {
			pollMS = 10
		}
	}
	cfg.PollInterval = time.Duration(pollMS) * time.Millisecond

	// Bind address: honour BINDING env var, default to localhost.
	cfg.Bind = os.Getenv("BINDING")
	if cfg.Bind == "" {
		cfg.Bind = "127.0.0.1"
	}

	// Validate table name: only alphanumeric and underscores allowed.
	for _, c := range cfg.TableName {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			log.Fatalf("invalid table name %q: only alphanumeric characters and underscores are allowed", cfg.TableName)
		}
	}

	if len(cfg.AllowOrigins) == 0 {
		log.Fatal("--allow-origin is required (e.g. --allow-origin http://localhost:3000)")
	}

	// FINESSE_SIGNING_KEY is the hex-encoded Turbo signed stream verifier key,
	// derived from SECRET_KEY_BASE via PBKDF2 by the Ruby wrapper.
	// FINESSE_SIGNING_KEYS adds more (comma-separated) so keys can rotate
	// without every process restarting at once.
	var err error
	cfg.SigningKeys, err = parseSigningKeys(os.Getenv("FINESSE_SIGNING_KEY"), os.Getenv("FINESSE_SIGNING_KEYS"))
	if err != nil {
		log.Fatal(err)
	}

	return cfg
}

// parseSigningKeys decodes the union of a single hex key and a comma-separated
// list of hex keys. At least one key is required.
func parseSigningKeys(single, list string) ([][]byte, error) {
	var keys [][]byte
	candidates := []string{single}
	candidates = append(candidates, strings.Split(list, ",")...)
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		key, err := hex.DecodeString(c)
		if err != nil {
			return nil, fmt.Errorf("signing keys must be valid hex: %w", err)
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, errors.New("FINESSE_SIGNING_KEY or FINESSE_SIGNING_KEYS environment variable is required")
	}
	return keys, nil
}
