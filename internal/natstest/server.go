// Package natstest runs an in-process NATS server with JetStream for tests, so
// the archivist's end-to-end tests need no external server.
package natstest

import (
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// StartJetStream starts an in-process NATS server with JetStream enabled, backed
// by a per-test temporary store directory, and returns its client URL together
// with a cleanup function.
func StartJetStream(t *testing.T) (url string, cleanup func()) {
	t.Helper()

	opts := &server.Options{
		JetStream: true,
		StoreDir:  t.TempDir(),
		Host:      "127.0.0.1",
		Port:      -1, // pick a random free port
		NoLog:     true,
		NoSigs:    true,
	}

	ns, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("natstest: new server: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(10 * time.Second) {
		ns.Shutdown()
		t.Fatal("natstest: server not ready for connections")
	}
	return ns.ClientURL(), ns.Shutdown
}
