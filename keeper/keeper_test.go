package keeper

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/impire-io/soulstream/identity"
	"github.com/impire-io/soulstream/realm"
	"github.com/impire-io/soulstream/topic"

	"github.com/impire-io/soulstream-archivist/archive"
	"github.com/impire-io/soulstream-archivist/internal/natstest"
)

func client(t *testing.T, url, persona string, key *identity.SigningKey) *realm.Client {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// A missing key must leave Signer unset: a typed-nil *SigningKey in the
	// interface field is refused at construction (soulstream 017's guard).
	cfg := realm.Config{Realm: "test-realm", Persona: persona}
	if key != nil {
		cfg.Signer = key
	}
	c, err := realm.NewClient(context.Background(), nc, cfg)
	if err != nil {
		nc.Close()
		t.Fatalf("client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestArchivistEndToEnd is the whole reason this repository exists, played out:
// keep ops while they are live, survive the rollup that destroys them, and hand
// them back as verifying evidence over the memory convention — all through the
// soulstream library's public surface.
func TestArchivistEndToEnd(t *testing.T) {
	ctx := context.Background()
	url, shutdown := natstest.StartJetStream(t)
	t.Cleanup(shutdown)

	ownerKey, err := identity.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	owner := client(t, url, "owner", ownerKey)
	if _, err := owner.Provision(ctx); err != nil {
		t.Fatalf("provision: %v", err)
	}

	// The conversation: a turn, a challenge, the mark that settled it, and the
	// conversation moving on — making the mark interior, which is exactly what a
	// rollup consumes without keeping its id.
	h, err := topic.StartTopic(ctx, owner, topic.StartTopicInput{Name: "the decision", SubjectMatter: "cadence"})
	if err != nil {
		t.Fatal(err)
	}
	turnID, err := h.PostTurn(ctx, "weekly cadence it is")
	if err != nil {
		t.Fatal(err)
	}
	commentID, err := h.AddComment(ctx, "monthly instead?", turnID)
	if err != nil {
		t.Fatal(err)
	}
	markID, err := h.Resolve(ctx, commentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.PostTurn(ctx, "moving on"); err != nil {
		t.Fatal(err)
	}

	// The archivist starts keeping. DeliverAll picks up the backlog too.
	store, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	arch := client(t, url, "archivist", nil)
	kctx, stopKeeper := context.WithCancel(ctx)
	defer stopKeeper()
	keeperDone := make(chan error, 1)
	go func() { keeperDone <- Run(kctx, arch, store, nil) }()

	// announce + baseline + turn + comment + resolve + turn = 6 ops kept.
	waitFor(t, "backlog archived", func() bool { n, _ := store.Count(); return n >= 6 })

	kr := &identity.Keyring{Keys: map[string][]string{"owner": {ownerKey.PublicKey()}}}
	kept, ok := store.Get(h.Path(), markID)
	if !ok {
		t.Fatal("the mark was not kept")
	}
	if v, err := topic.VerifyExhibit(kept, kr); err != nil || v != topic.SigVerified {
		t.Fatalf("kept exhibit verdict = %v (%v), want verified", v, err)
	}

	// Rollup destroys the mark; the stream forgets, the archive does not.
	if _, err := h.Rollup(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if _, err := topic.CaptureExhibit(ctx, owner, h.Path(), markID); !errors.Is(err, topic.ErrOpNotLive) {
		t.Fatalf("after rollup: %v, want ErrOpNotLive", err)
	}

	// Serve the archive as a witness; the asker recovers the destroyed op as
	// verifying evidence, and queries come back cited and graded.
	st, err := store.LoadState()
	if err != nil || st.CoverageFrom.IsZero() {
		t.Fatalf("state = %+v (%v)", st, err)
	}
	wctx, stopWitness := context.WithCancel(ctx)
	defer stopWitness()
	go func() { _ = topic.RespondMemory(wctx, arch, Witness(store, st.CoverageFrom)) }()

	var fetched *topic.ExhibitResult
	waitFor(t, "witness serves the fetch", func() bool {
		fetched, err = topic.FetchExhibit(ctx, owner, h.Path(), markID, 300*time.Millisecond, kr)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		return fetched != nil
	})
	if fetched.Verdict != topic.SigVerified || fetched.Source != "archivist" {
		t.Fatalf("fetched = verdict %s from %s, want verified from archivist", fetched.Verdict, fetched.Source)
	}
	if topic.GradeForVerdict(fetched.Verdict) != topic.GradeProvenance {
		t.Error("a verifying recovered exhibit must grade fact-with-provenance")
	}

	var res *topic.MemoryResult
	waitFor(t, "witness answers the query", func() bool {
		res, err = topic.MemoryQuery(ctx, owner, topic.MemoryQueryInput{
			Query: "weekly cadence", Timeout: 300 * time.Millisecond,
		}, kr)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		return len(res.Answers) > 0
	})
	ans := res.Answers[0]
	if ans.Witness != "archivist" || ans.CoverageFrom.IsZero() {
		t.Errorf("answer attribution/coverage: %+v", ans)
	}
	foundFact := false
	for _, cit := range ans.Citations {
		if cit.Grade == topic.GradeFact {
			foundFact = true // the turn survives baked — the asker checked
		}
	}
	if !foundFact {
		t.Errorf("expected a fact-graded citation, got %+v", ans.Citations)
	}

	// Scope hints are honoured: an unrelated topic pattern silences the witness.
	res, err = topic.MemoryQuery(ctx, owner, topic.MemoryQueryInput{
		Query: "weekly cadence", Topics: []string{"unrelated-*"}, Timeout: 300 * time.Millisecond,
	}, kr)
	if err != nil {
		t.Fatalf("scoped query: %v", err)
	}
	if len(res.Answers) != 0 {
		t.Errorf("out-of-scope query should get silence, got %+v", res.Answers)
	}

	// Restart resumes without loss or duplication and keeps the coverage stamp.
	stopKeeper()
	if err := <-keeperDone; err != nil {
		t.Fatalf("keeper stopped with: %v", err)
	}
	before, _ := store.Count()
	if _, err := h.PostTurn(ctx, "one more thing"); err != nil {
		t.Fatal(err)
	}
	k2, stop2 := context.WithCancel(ctx)
	defer stop2()
	go func() { _ = Run(k2, arch, store, nil) }()
	waitFor(t, "resumed keeper catches the new op", func() bool { n, _ := store.Count(); return n == before+1 })
	st2, _ := store.LoadState()
	if !st2.CoverageFrom.Equal(st.CoverageFrom) {
		t.Error("coverage stamp must survive restarts")
	}
}
