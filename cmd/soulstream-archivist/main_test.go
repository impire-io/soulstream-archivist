package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/impire-io/soulstream-archivist/internal/version"
)

// TestVersion proves both the --version flag and the bare "version" subcommand
// answer with the build version and exit 0 — without connecting to a realm, so a
// broken configuration never hides the diagnostic.
func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"version"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, out := captureRun(t, args)
			if code != 0 {
				t.Errorf("run(%v) exit = %d, want 0", args, code)
			}
			if strings.TrimSpace(out) != version.Version {
				t.Errorf("run(%v) printed %q, want %q", args, strings.TrimSpace(out), version.Version)
			}
		})
	}
}

// captureRun runs the daemon's entry point with os.Stdout redirected to a pipe,
// returning the exit code and everything written to stdout.
func captureRun(t *testing.T, args []string) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	code := run(args)
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return code, string(out)
}
