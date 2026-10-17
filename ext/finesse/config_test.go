package main

import (
	"os"
	"os/exec"
	"testing"
)

// TestParseConfig_MissingAllowOrigin verifies that the server refuses to start
// without --allow-origin. ParseConfig calls log.Fatal, so we run it in a
// subprocess and assert a non-zero exit.
func TestParseConfig_MissingAllowOrigin(t *testing.T) {
	if os.Getenv("TEST_PARSE_CONFIG") == "1" {
		ParseConfig()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestParseConfig_MissingAllowOrigin$")
	cmd.Env = append(os.Environ(),
		"TEST_PARSE_CONFIG=1",
		"FINESSE_SIGNING_KEY=deadbeef",
	)
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected ParseConfig to exit non-zero without --allow-origin")
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() == 0 {
			t.Fatal("expected non-zero exit code")
		}
	}
}

// TestParseConfig_MissingSigningKey verifies that the server refuses to start
// without FINESSE_SIGNING_KEY.
func TestParseConfig_MissingSigningKey(t *testing.T) {
	if os.Getenv("TEST_PARSE_CONFIG") == "1" {
		ParseConfig()
		return
	}

	cmd := exec.Command(os.Args[0],
		"-test.run=^TestParseConfig_MissingSigningKey$",
		"--", "--allow-origin", "http://localhost:3000",
	)
	cmd.Env = []string{"TEST_PARSE_CONFIG=1", "PATH=" + os.Getenv("PATH")}
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected ParseConfig to exit non-zero without FINESSE_SIGNING_KEY")
	}
}

// TestParseConfig_BadSigningKeysHex verifies that one bad entry in
// FINESSE_SIGNING_KEYS stops startup instead of silently dropping the key.
func TestParseConfig_BadSigningKeysHex(t *testing.T) {
	if os.Getenv("TEST_PARSE_CONFIG") == "1" {
		ParseConfig()
		return
	}

	cmd := exec.Command(os.Args[0],
		"-test.run=^TestParseConfig_BadSigningKeysHex$",
		"--", "--allow-origin", "http://localhost:3000",
	)
	cmd.Env = []string{"TEST_PARSE_CONFIG=1", "PATH=" + os.Getenv("PATH"), "FINESSE_SIGNING_KEYS=deadbeef,nothex"}
	if err := cmd.Run(); err == nil {
		t.Fatal("expected ParseConfig to exit non-zero for invalid hex in FINESSE_SIGNING_KEYS")
	}
}

func TestParseSigningKeys_UnionOfBothVariables(t *testing.T) {
	keys, err := parseSigningKeys("aa", "bb, cc,")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 || keys[0][0] != 0xaa || keys[1][0] != 0xbb || keys[2][0] != 0xcc {
		t.Fatalf("keys = %x", keys)
	}
	if _, err := parseSigningKeys("", " , "); err == nil {
		t.Fatal("expected an error when no key is given")
	}
}
