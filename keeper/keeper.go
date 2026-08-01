// Package keeper is the archivist's habit: capture every operation on the
// realm's op-log verbatim while it is live — because retention is not
// retrofittable — and serve what is kept through the memory convention's public
// witness surface. Built exclusively on the soulstream library's exported
// surface; there is nothing privileged here, only diligence.
//
// The package is public on purpose: it is the archivist's embed seam. A
// process that already holds a *realm.Client (a bundled distribution such
// as soulnode) runs the archivist in-process with Run + Witness +
// topic.RespondMemory — exactly what cmd/soulstream-archivist wires.
package keeper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/impire-io/soulstream/realm"
	"github.com/impire-io/soulstream/record"
	"github.com/impire-io/soulstream/topic"

	"github.com/impire-io/soulstream-archivist/archive"
)

// Run archives the realm's op-log into store until ctx ends: an ordered read of
// every operation subject, each message captured byte-for-byte as an exhibit
// (verbatim bytes are what keep the author's signature verifying forever). It
// resumes from the store's recorded sequence, so restarts neither lose nor
// duplicate anything. onKept, if non-nil, is called after each kept op.
//
// The first run stamps the store's coverage start: the honest, conservative
// "my notes start here". The backlog the stream still holds is captured too —
// coverage may in fact reach further back; the declaration never overclaims.
func Run(ctx context.Context, c *realm.Client, store *archive.Store, onKept func(subject string, seq uint64)) error {
	st, err := store.LoadState()
	if err != nil {
		return err
	}
	if st.CoverageFrom.IsZero() {
		st.CoverageFrom = time.Now().UTC()
		if err := store.SaveState(st); err != nil {
			return err
		}
	}

	stream, err := c.JetStream().Stream(ctx, realm.StreamName)
	if err != nil {
		return fmt.Errorf("keeper: the realm's stream is not reachable (is the realm provisioned?): %w", err)
	}
	cfg := jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{realm.StreamSubject},
		DeliverPolicy:  jetstream.DeliverAllPolicy,
	}
	if st.LastSeq > 0 {
		cfg.DeliverPolicy = jetstream.DeliverByStartSequencePolicy
		cfg.OptStartSeq = st.LastSeq + 1
	}
	cons, err := stream.OrderedConsumer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("keeper: create consumer: %w", err)
	}
	it, err := cons.Messages()
	if err != nil {
		return fmt.Errorf("keeper: consume: %w", err)
	}
	defer it.Stop()
	go func() { <-ctx.Done(); it.Stop() }()

	for {
		msg, err := it.Next()
		if err != nil {
			if errors.Is(err, jetstream.ErrMsgIteratorClosed) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("keeper: read op: %w", err)
		}
		md, err := msg.Metadata()
		if err != nil {
			continue
		}
		if ex, ok := exhibitOf(c.Realm(), msg); ok {
			if err := store.Put(ex); err != nil {
				return err // a store that cannot write is a keeper that must stop, loudly
			}
			if onKept != nil {
				onKept(msg.Subject(), md.Sequence.Stream)
			}
		}
		st.LastSeq = md.Sequence.Stream
		if err := store.SaveState(st); err != nil {
			return err
		}
	}
}

// exhibitOf captures one stream message verbatim as an exhibit. Only the topic
// op/info subjects are archived; anything else on the stream is not ours to keep.
func exhibitOf(realmName string, msg jetstream.Msg) (record.Exhibit, bool) {
	subject := msg.Subject()
	var binding string
	switch {
	case strings.HasPrefix(subject, topic.OpsSubjectPrefix):
		binding = strings.TrimPrefix(subject, topic.OpsSubjectPrefix)
	case strings.HasPrefix(subject, topic.InfoSubjectPrefix):
		binding = strings.TrimPrefix(subject, topic.InfoSubjectPrefix)
	default:
		return record.Exhibit{}, false
	}
	headers := make(map[string][]string, len(msg.Headers()))
	for k, v := range msg.Headers() {
		headers[k] = append([]string(nil), v...)
	}
	return record.Exhibit{
		Version: record.ExhibitVersion,
		Realm:   realmName,
		Binding: binding,
		Subject: subject,
		Headers: headers,
		Payload: append([]byte(nil), msg.Data()...),
	}, true
}

// Witness serves the archive over the memory convention: answers from a plain
// search of the kept operations, exhibits straight from the store, coverage
// declared from the store's own state. Both capabilities, one habit.
func Witness(store *archive.Store, coverageFrom time.Time) topic.MemoryWitness {
	return topic.MemoryWitness{
		CoverageFrom: coverageFrom,
		Answer: func(q topic.MemoryQueryRequest) []topic.MemoryAnswerDraft {
			hits, err := store.Search(q.Query, 5)
			if err != nil {
				return nil
			}
			var drafts []topic.MemoryAnswerDraft
			for _, h := range hits {
				if !inScope(h, q) {
					continue
				}
				drafts = append(drafts, topic.MemoryAnswerDraft{
					Answer: fmt.Sprintf("%s said in %s (%s, %s): %s",
						h.Author, h.Topic, h.Type, h.Ts.Format("2006-01-02"), h.Snippet),
					Citations: []topic.MemoryCitation{{Topic: h.Topic, OpID: h.OpID}},
				})
			}
			return drafts
		},
		Fetch: store.Get,
	}
}

// inScope honours the asker's relevance hints: topic patterns as substring
// matches (a trailing * is stripped), the after-moment as a floor. Hints, not
// law — the asker's protection is grading, not our filtering.
func inScope(h archive.Hit, q topic.MemoryQueryRequest) bool {
	if !q.After.IsZero() && h.Ts.Before(q.After) {
		return false
	}
	if len(q.Topics) == 0 {
		return true
	}
	for _, pat := range q.Topics {
		pat = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(pat), "*"))
		if pat == "" || strings.Contains(strings.ToLower(h.Topic), pat) {
			return true
		}
	}
	return false
}
