package main

import (
	"encoding/hex"
	"flag"
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
	TableName    string
	PollInterval time.Duration
	AllowOrigins []string
	SigningKey    []byte
}

// ParseConfig reads CLI flags and returns a Config.
func ParseConfig() Config {
	cfg := Config{}
	var pollMS int
	flag.IntVar(&cfg.Port, "port", 4000, "HTTP listen port")
	flag.StringVar(&cfg.DBPath, "db-path", "storage/development_cable.sqlite3", "path to SolidCable SQLite database")
	flag.StringVar(&cfg.TableName, "table-name", "solid_cable_messages", "SolidCable messages table name")
	flag.IntVar(&pollMS, "poll-interval", 10, "database poll interval in milliseconds")
	var allowOrigins stringSlice
	flag.Var(&allowOrigins, "allow-origin", "allowed origin (may be repeated, e.g. --allow-origin http://localhost:3000 --allow-origin http://192.168.1.10:3000)")
	flag.Parse()
	cfg.AllowOrigins = allowOrigins
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
	signingKeyHex := os.Getenv("FINESSE_SIGNING_KEY")
	if signingKeyHex == "" {
		log.Fatal("FINESSE_SIGNING_KEY environment variable is required")
	}
	var err error
	cfg.SigningKey, err = hex.DecodeString(signingKeyHex)
	if err != nil {
		log.Fatalf("FINESSE_SIGNING_KEY must be valid hex: %v", err)
	}

	return cfg
}
