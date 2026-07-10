package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Dir returns the default snapshots directory under project cwd: .dbx/snapshots.
func Dir(cwd string) string {
	return filepath.Join(cwd, ".dbx", "snapshots")
}

// SnapshotPath returns the file path for a validated snapshot name.
func SnapshotPath(dir, name string) string {
	return filepath.Join(dir, name+".json")
}

// Save writes a snapshot envelope atomically.
// If force is false and the file exists, returns an error.
// Returns the absolute (or cleaned) path written.
func Save(dir string, s Snapshot, force bool) (string, error) {
	if err := ValidateName(s.Name); err != nil {
		return "", err
	}
	if s.Type == "" {
		s.Type = TypeSnapshot
	}
	// New custom directories are private from the start. Existing custom
	// directories are intentionally left unchanged: callers may own/share them.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create snapshots dir: %w", err)
	}

	path := SnapshotPath(dir, s.Name)
	if !force {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return "", fmt.Errorf("snapshot already exists: %s (use --force)", s.Name)
		} else if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("stat snapshot: %w", err)
		}
	}

	body, err := EncodeSnapshot(s)
	if err != nil {
		return "", fmt.Errorf("encode snapshot: %w", err)
	}
	if err := atomicWrite(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// Load reads and parses a snapshot by name from dir.
func Load(dir, name string) (Snapshot, error) {
	if err := ValidateName(name); err != nil {
		return Snapshot{}, err
	}
	path := SnapshotPath(dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{}, fmt.Errorf("snapshot not found: %s", name)
		}
		return Snapshot{}, fmt.Errorf("read snapshot: %w", err)
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return Snapshot{}, fmt.Errorf("parse snapshot %s: %w", name, err)
	}
	if s.Type != TypeSnapshot {
		return Snapshot{}, fmt.Errorf("invalid snapshot %s envelope type", name)
	}
	if s.Name != name {
		return Snapshot{}, fmt.Errorf("snapshot %s has mismatched name", name)
	}
	if s.CreatedAt.IsZero() {
		return Snapshot{}, fmt.Errorf("snapshot %s has empty created_at", name)
	}
	if len(s.Data) == 0 {
		return Snapshot{}, fmt.Errorf("snapshot %s has empty data", name)
	}
	if !json.Valid(s.Data) {
		return Snapshot{}, fmt.Errorf("snapshot %s has invalid data", name)
	}
	return s, nil
}

// Entry is a list row for a snapshot on disk.
type Entry struct {
	Name      string
	CreatedAt time.Time
	// Path is the full file path.
	Path string
}

// List returns snapshot entries sorted by name.
// If an envelope cannot be parsed, CreatedAt is zero and Name is still included.
func List(dir string) ([]Entry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list snapshots: %w", err)
	}

	var out []Entry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		base := strings.TrimSuffix(name, ".json")
		if err := ValidateName(base); err != nil {
			// Skip non-snapshot filenames (e.g. junk).
			continue
		}
		path := filepath.Join(dir, name)
		ent := Entry{Name: base, Path: path}
		if data, err := os.ReadFile(path); err == nil {
			var s Snapshot
			if json.Unmarshal(data, &s) == nil {
				ent.CreatedAt = s.CreatedAt
			}
		}
		out = append(out, ent)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// atomicWrite writes data to path via a temp file in the same directory + rename.
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
	// Ensure cleanup on any failure path before rename succeeds.
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
		return fmt.Errorf("rename snapshot: %w", err)
	}
	success = true
	return nil
}

// EnsurePrivateDir creates a dbx-owned directory and makes it owner-only.
// Callers must use this only for default directories under the project cwd,
// never for an explicitly supplied snapshot --dir.
func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}
