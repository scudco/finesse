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
