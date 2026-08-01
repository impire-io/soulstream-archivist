// Command soulstream-archivist is a realm's historian: an ordinary persona whose
// habit is keeping every operation while it is live and serving what it kept
// through the memory convention. No special standing, no privileged access —
// just the classmate with the thickest notebook.
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/impire-io/soulstream/identity"
	"github.com/impire-io/soulstream/realm"
	"github.com/impire-io/soulstream/topic"

	"github.com/impire-io/soulstream-archivist/archive"
	"github.com/impire-io/soulstream-archivist/internal/version"
	"github.com/impire-io/soulstream-archivist/keeper"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("soulstream-archivist", flag.ContinueOnError)
	ctxName := fs.String("context", os.Getenv("SOULSTREAM_CONTEXT"), "named NATS context")
	realmName := fs.String("realm", os.Getenv("SOULSTREAM_REALM"), "realm name")
	persona := fs.String("persona", envOr("SOULSTREAM_PERSONA", "archivist"), "persona this archivist serves as")
	keyFile := fs.String("key-file", os.Getenv("SOULSTREAM_KEY_FILE"), "signing-seed file (as written by 'soulstream key init'; absent means answers go unsigned)")
	dataDir := fs.String("data-dir", os.Getenv("SOULSTREAM_ARCHIVIST_DATA"), "archive directory (default: user config dir /soulstream-archivist/<realm>)")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// Version must always answer — via the --version flag or the bare "version"
	// subcommand — before any configuration can be missing or wrong.
	if *showVersion || fs.Arg(0) == "version" {
		fmt.Println(version.Version)
		return 0
	}
	if *realmName == "" {
		fmt.Fprintln(os.Stderr, "soulstream-archivist: a realm is required (--realm or SOULSTREAM_REALM)")
		return 2
	}
	if *dataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "soulstream-archivist: no data dir: %v\n", err)
			return 2
		}
		*dataDir = filepath.Join(base, "soulstream-archivist", *realmName)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	store, err := archive.Open(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "soulstream-archivist: %v\n", err)
		return 1
	}
	signer, err := loadKey(*keyFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "soulstream-archivist: %v\n", err)
		return 1
	}
	if signer == nil {
		fmt.Println("no signing key — answers and exhibits will be served unsigned (testimony-grade transport)")
	}

	// A missing key must leave Signer unset: a typed-nil *SigningKey in the
	// interface field is refused at construction (soulstream 017's guard).
	cfg := realm.Config{ContextName: *ctxName, Realm: *realmName, Persona: *persona}
	if signer != nil {
		cfg.Signer = signer
	}
	c, err := realm.Connect(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "soulstream-archivist: %v\n", err)
		return 1
	}
	defer func() { _ = c.Close() }()

	st, err := store.LoadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "soulstream-archivist: %v\n", err)
		return 1
	}
	if !st.CoverageFrom.IsZero() {
		fmt.Printf("archiving realm %q as %q — coverage since %s, resuming after seq %d\n",
			*realmName, *persona, st.CoverageFrom.Format("2006-01-02 15:04"), st.LastSeq)
	} else {
		fmt.Printf("archiving realm %q as %q — coverage starts NOW (the stream's remaining backlog is kept too)\n",
			*realmName, *persona)
	}

	keeperErr := make(chan error, 1)
	go func() {
		keeperErr <- keeper.Run(ctx, c, store, func(subject string, seq uint64) {
			fmt.Printf("kept %s (seq %d)\n", subject, seq)
		})
	}()

	// Serve the archive over the memory convention until interrupted. Coverage is
	// re-read so the witness declares what the state file actually says.
	st, _ = store.LoadState()
	w := keeper.Witness(store, st.CoverageFrom)
	w.OnServed = func(kind string, n int, err error) {
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve %s failed: %v\n", kind, err)
			return
		}
		fmt.Printf("served %s: %d\n", kind, n)
	}
	witnessErr := make(chan error, 1)
	go func() { witnessErr <- topic.RespondMemory(ctx, c, w) }()

	select {
	case err := <-keeperErr:
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "soulstream-archivist: keeper stopped: %v\n", err)
			return 1
		}
	case err := <-witnessErr:
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "soulstream-archivist: witness stopped: %v\n", err)
			return 1
		}
	case <-ctx.Done():
	}
	return 0
}

// loadKey reads a signing-seed file in the exact format `soulstream key init`
// writes (base64 of the Ed25519 seed). Absent path or file means unsigned.
func loadKey(path string) (*identity.SigningKey, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("key file %s is not base64: %w", path, err)
	}
	return identity.SigningKeyFromSeed(seed)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
