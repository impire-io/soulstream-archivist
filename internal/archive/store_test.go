package archive

import (
	"strings"
	"testing"
	"time"

	"github.com/impire-io/soulstream/record"
)

func testExhibit(t *testing.T, topicPath, body string) record.Exhibit {
	t.Helper()
	rec := record.Record{
		ID:        record.NewID(),
		Author:    "daan",
		Type:      "turn.post",
		Timestamp: time.Now().UTC().Truncate(time.Second),
		Payload:   []byte(`{"body":"` + body + `"}`),
	}
	headers, payload, err := rec.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return record.Exhibit{
		Version: record.ExhibitVersion,
		Realm:   "test-realm",
		Binding: topicPath,
		Subject: "SOULSTREAM.TOPICS.OPS." + topicPath,
		Headers: headers,
		Payload: payload,
	}
}

func TestStorePutGetRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ex := testExhibit(t, "vat-q2-x7m2", "weekly cadence")
	rec, _ := ex.Record()
	if err := s.Put(ex); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.Put(ex); err != nil {
		t.Fatalf("idempotent re-put: %v", err)
	}
	got, ok := s.Get("vat-q2-x7m2", rec.ID)
	if !ok {
		t.Fatal("get: not found")
	}
	a, _ := got.Marshal()
	b, _ := ex.Marshal()
	if string(a) != string(b) {
		t.Error("round-trip changed the document")
	}
	if _, ok := s.Get("vat-q2-x7m2", record.NewID()); ok {
		t.Error("unknown op must not be found")
	}
	if _, ok := s.Get("no-such-topic", rec.ID); ok {
		t.Error("wrong topic must not be found")
	}
	if n, err := s.Count(); err != nil || n != 1 {
		t.Errorf("count = %d (%v), want 1", n, err)
	}
}

func TestStoreRefusesHostileNames(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bad := testExhibit(t, "vat-q2", "x")
	bad.Binding = "../escape"
	if err := s.Put(bad); err == nil {
		t.Error("hostile binding must be refused")
	}
	if _, ok := s.Get("../escape", "x"); ok {
		t.Error("hostile get must miss")
	}
	if _, ok := s.Get("vat-q2", "../../state"); ok {
		t.Error("hostile op-id must miss")
	}
}

func TestStoreSearch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(testExhibit(t, "vat-q2-x7m2", "weekly cadence it is")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(testExhibit(t, "onboarding-a1b2", "welcome aboard")); err != nil {
		t.Fatal(err)
	}

	hits, err := s.Search("CADENCE", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search cadence: %d hits (%v)", len(hits), err)
	}
	h := hits[0]
	if h.Topic != "vat-q2-x7m2" || h.Author != "daan" || h.Type != "turn.post" {
		t.Errorf("hit = %+v", h)
	}
	if !strings.Contains(h.Snippet, "weekly cadence") {
		t.Errorf("snippet = %q", h.Snippet)
	}
	if hits, _ := s.Search("vat", 5); len(hits) != 1 {
		t.Errorf("topic-name search: %d hits, want 1", len(hits))
	}
	if hits, _ := s.Search("quantum", 5); len(hits) != 0 {
		t.Errorf("miss search: %d hits, want 0", len(hits))
	}
	if hits, _ := s.Search("", 1); len(hits) != 1 {
		t.Errorf("limit: %d hits, want 1", len(hits))
	}
}

func TestStoreState(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.LoadState()
	if err != nil || !st.CoverageFrom.IsZero() || st.LastSeq != 0 {
		t.Fatalf("fresh state = %+v (%v)", st, err)
	}
	want := State{CoverageFrom: time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC), LastSeq: 42}
	if err := s.SaveState(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadState()
	if err != nil || !got.CoverageFrom.Equal(want.CoverageFrom) || got.LastSeq != 42 {
		t.Errorf("state round-trip = %+v (%v)", got, err)
	}
}
