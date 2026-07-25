// Package archive is the archivist's private store: exhibits captured verbatim
// while they were live, kept as plain files. The memory convention never sees
// this shape — it is served through the public witness surface — so it stays as
// boring as possible: one JSON document per operation, one JSON file of state.
package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/impire-io/soulstream/record"
)

// Store is a directory of kept exhibits plus the keeper's resume state.
type Store struct {
	root string
}

// Open creates (or reopens) a store rooted at dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "exhibits"), 0o700); err != nil {
		return nil, fmt.Errorf("archive: create store: %w", err)
	}
	return &Store{root: dir}, nil
}

// Put keeps one exhibit, idempotently: the op-id names the file, and keeping the
// same op twice is a harmless overwrite with identical bytes.
func (s *Store) Put(ex record.Exhibit) error {
	rec, err := ex.Record()
	if err != nil {
		return fmt.Errorf("archive: refusing an exhibit without a readable operation: %w", err)
	}
	seg, err := safeSegment(ex.Binding)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.root, "exhibits", seg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("archive: create topic dir: %w", err)
	}
	data, err := ex.Marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, rec.ID+".json"), data, 0o600); err != nil {
		return fmt.Errorf("archive: write exhibit: %w", err)
	}
	return nil
}

// Get returns the kept exhibit for one operation, if this archive holds it.
func (s *Store) Get(topicPath, opID string) (record.Exhibit, bool) {
	seg, err := safeSegment(topicPath)
	if err != nil {
		return record.Exhibit{}, false
	}
	if _, err := parseOpID(opID); err != nil {
		return record.Exhibit{}, false
	}
	data, err := os.ReadFile(filepath.Join(s.root, "exhibits", seg, opID+".json"))
	if err != nil {
		return record.Exhibit{}, false
	}
	ex, err := record.ParseExhibit(data)
	if err != nil {
		return record.Exhibit{}, false // a corrupted file is not evidence
	}
	return ex, true
}

// Count reports how many exhibits the archive holds.
func (s *Store) Count() (int, error) {
	n := 0
	err := s.walk(func(record.Exhibit) error { n++; return nil })
	return n, err
}

// Hit is one search result: enough to cite and to summarise honestly.
type Hit struct {
	Topic   string
	OpID    string
	Author  string
	Type    string
	Ts      time.Time
	Snippet string
}

// Search walks the archive for operations whose content, author, type, or topic
// contains the query (case-insensitive). Deliberately unsophisticated: the
// convention grades answers, it does not require clever retrieval. Results are
// capped at limit (≤ 0 means 5).
func (s *Store) Search(query string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 5
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var hits []Hit
	err := s.walk(func(ex record.Exhibit) error {
		if len(hits) >= limit {
			return fs.SkipAll
		}
		rec, err := ex.Record()
		if err != nil {
			return nil // skip corrupted files, keep serving
		}
		haystack := strings.ToLower(ex.Binding + " " + rec.Author + " " + rec.Type + " " + string(rec.Payload))
		if q != "" && !strings.Contains(haystack, q) {
			return nil
		}
		hits = append(hits, Hit{
			Topic: ex.Binding, OpID: rec.ID, Author: rec.Author, Type: rec.Type,
			Ts: rec.Timestamp, Snippet: snippetOf(rec.Payload),
		})
		return nil
	})
	return hits, err
}

func (s *Store) walk(fn func(record.Exhibit) error) error {
	root := filepath.Join(s.root, "exhibits")
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		ex, perr := record.ParseExhibit(data)
		if perr != nil {
			return nil
		}
		return fn(ex)
	})
}

// snippetOf pulls the human part of a payload when it has one (body or title),
// else the raw payload head — for the witness's honest one-line summary.
func snippetOf(payload []byte) string {
	var probe struct {
		Body  string `json:"body"`
		Title string `json:"title"`
	}
	text := ""
	if json.Unmarshal(payload, &probe) == nil {
		if probe.Body != "" {
			text = probe.Body
		} else if probe.Title != "" {
			text = probe.Title
		}
	}
	if text == "" {
		text = string(payload)
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 120 {
		text = text[:120] + "…"
	}
	return text
}

// State is the keeper's durable position: when its coverage started (the honest
// "my notes start here" it declares in answers) and the last stream sequence it
// has archived (resume point).
type State struct {
	CoverageFrom time.Time `json:"coverage_from"`
	LastSeq      uint64    `json:"last_seq"`
}

func (s *Store) statePath() string { return filepath.Join(s.root, "state.json") }

// LoadState reads the keeper state; a fresh store returns a zero State.
func (s *Store) LoadState() (State, error) {
	data, err := os.ReadFile(s.statePath())
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("archive: read state: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("archive: state file is corrupt: %w", err)
	}
	return st, nil
}

// SaveState persists the keeper state atomically enough for a single keeper.
func (s *Store) SaveState(st State) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.statePath(), data, 0o600); err != nil {
		return fmt.Errorf("archive: write state: %w", err)
	}
	return nil
}

// safeSegment admits exactly the topic-path shape (slug segments joined by dots)
// as a directory name — anything else is refused, which also rules out path
// tricks from hostile bindings.
func safeSegment(binding string) (string, error) {
	if binding == "" {
		return "", fmt.Errorf("archive: empty topic binding")
	}
	for _, r := range binding {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
		default:
			return "", fmt.Errorf("archive: binding %q is not a topic path", binding)
		}
	}
	if strings.Contains(binding, "..") || strings.HasPrefix(binding, ".") || strings.HasSuffix(binding, ".") {
		return "", fmt.Errorf("archive: binding %q is not a topic path", binding)
	}
	return binding, nil
}

func parseOpID(opID string) (string, error) {
	if opID == "" || strings.ContainsAny(opID, "/\\.") {
		return "", fmt.Errorf("archive: op-id %q is not an op-id", opID)
	}
	return opID, nil
}
