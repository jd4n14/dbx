// Package history persists successful dbx query runs as a bounded JSONL file
// under <project>/.dbx/history.jsonl. Entries are append-only on disk so a
// crash mid-write cannot corrupt prior history; once the file exceeds the cap
// the oldest entries are trimmed away in a single rewrite.
package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultLimit caps retained entries per project. Older entries are dropped
// oldest-first once the cap is exceeded.
const DefaultLimit = 100

// Type is the envelope marker for JSONL entries (kept constant so the on-disk
// format stays self-identifying if we ever need to evolve it).
const Type = "history_entry"

// Path returns the JSONL path under cwd: <cwd>/.dbx/history.jsonl.
func Path(cwd string) string {
	return filepath.Join(cwd, ".dbx", "history.jsonl")
}

// Entry is one successful query run; persisted as a single line.
type Entry struct {
	Type       string    `json:"type"`
	Timestamp  time.Time `json:"ts"`
	Connection string    `json:"connection,omitempty"`
	SQL        string    `json:"sql"`
	Rows       int       `json:"rows"`
	Bytes      int       `json:"bytes"`
	DurationMs int64     `json:"duration_ms"`
}

// Append writes e to the JSONL file under cwd, applying limit (capping the
// retained oldest entries). A non-positive limit falls back to DefaultLimit.
// A blank SQL is a no-op so we never persist empty placeholders.
func Append(cwd string, e Entry, limit int) error {
	if strings.TrimSpace(e.SQL) == "" {
		return nil
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	} else {
		e.Timestamp = e.Timestamp.UTC()
	}
	if e.Type == "" {
		e.Type = Type
	}
	if limit <= 0 {
		limit = DefaultLimit
	}

	path := Path(cwd)
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("create history dir: %w", err)
	}
	body, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode history entry: %w", err)
	}
	body = append(body, '\n')

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open history file: %w", err)
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return fmt.Errorf("write history line: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close history file: %w", err)
	}

	return trimToLimit(path, limit)
}

// ListedEntry is a UI-friendly view: includes a 1-based index where 1 is the
// newest entry.
type ListedEntry struct {
	Index int
	Entry
}

// List reads at most `limit` newest entries from the JSONL and returns them
// newest-first with 1-based indices. A non-positive limit falls back to
// DefaultLimit. Missing file yields (nil, nil) so callers can render empty.
func List(cwd string, limit int) ([]ListedEntry, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	path := Path(cwd)
	entries, err := readEntries(path)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	// Newest first. Append order is monotonically increasing on disk because
	// entries are appended chronologically, but rely on Timestamp so a clock
	// jump or replayed line still ends up in the right place.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	out := make([]ListedEntry, 0, len(entries))
	for i, e := range entries {
		out = append(out, ListedEntry{Index: i + 1, Entry: e})
	}
	return out, nil
}

// ShowByIndex resolves the entry at the 1-based index (1 = newest) and
// returns it.
func ShowByIndex(cwd string, index int) (Entry, error) {
	if index <= 0 {
		return Entry{}, fmt.Errorf("history index must be >= 1 (got %d)", index)
	}
	entries, err := List(cwd, 0)
	if err != nil {
		return Entry{}, err
	}
	if len(entries) == 0 {
		return Entry{}, fmt.Errorf("no history; run dbx query first")
	}
	if index > len(entries) {
		return Entry{}, fmt.Errorf("history index %d out of range (have %d)", index, len(entries))
	}
	return entries[index-1].Entry, nil
}

// Clear deletes the history file if present. Missing file is not an error.
func Clear(cwd string) error {
	err := os.Remove(Path(cwd))
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("remove history: %w", err)
}

// Count returns the number of retained entries (used by tests/smoke).
func Count(cwd string) int {
	entries, err := readEntries(Path(cwd))
	if err != nil {
		return 0
	}
	return len(entries)
}

// EnsurePrivateDir creates dir with private perms (0o700). Used for both
// <root>/.dbx and .dbx/history.jsonl so default history is owner-only.
func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

func readEntries(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read history: %w", err)
	}
	defer f.Close()

	var out []Entry
	scanner := bufio.NewScanner(f)
	// Allow up to 8 MiB SQL blobs per line; default 64 KiB is too small for
	// the kind of long queries a real backend test would store.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := bytesTrim(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			// Skip non-conforming lines so a partial write does not poison
			// the whole file.
			continue
		}
		out = append(out, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan history: %w", err)
	}
	return out, nil
}

func trimToLimit(path string, limit int) error {
	entries, err := readEntries(path)
	if err != nil {
		return err
	}
	if len(entries) <= limit {
		return nil
	}
	keep := entries[len(entries)-limit:]
	body, err := encodeLines(keep)
	if err != nil {
		return err
	}
	return atomicWrite(path, body, 0o600)
}

func encodeLines(entries []Entry) ([]byte, error) {
	var b strings.Builder
	b.Grow(len(entries) * 128)
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return nil, fmt.Errorf("encode history entry: %w", err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".dbx-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename history: %w", err)
	}
	success = true
	return nil
}

func bytesTrim(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\n' || b[j-1] == '\r') {
		j--
	}
	return b[i:j]
}
