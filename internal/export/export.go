// Package export writes a snapshot's data payload to disk in CSV or JSON
// Lines form, with optional JSON sidecar metadata.
//
// Design notes (Plan 008):
//
//   - Stdlib only. encoding/csv handles RFC 4180 quoting; JSONL is one
//     json.Marshal-encoded object per line, terminated by '\n'.
//   - Atomic writes: data is written to a tempfile in the same directory,
//     fsync'd, then renamed into place. When --json is enabled the sidecar
//     is written first so that a partial state can never claim a row count
//     the data file does not yet contain.
//   - Sidecar carries audit metadata only: snapshot id, source connection
//     alias, exported_at timestamp, row count, export format, dbx
//     version/commit. No query text, no connection secrets, no row data.
//   - Snapshot data is json.RawMessage; the loader normalizes the input
//     before we see it (compact JSON). We accept objects, arrays of
//     objects, and primitives. Anything else yields a friendly error.
package export

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Format identifies an on-disk export encoding.
type Format string

// Supported export formats.
const (
	FormatCSV   Format = "csv"
	FormatJSONL Format = "jsonl"
)

// SidecarVersion is stamped into the JSON sidecar for forward compat.
const SidecarVersion = "dbx.export/v1"

// Options control a single export. Data is the normalized snapshot payload
// (json.RawMessage). Connection is the source alias; it is metadata only and
// may be empty. Version is the dbx build identifier embedded in the sidecar.
// OutputDir controls the temp/rename target; when empty, os.TempDir is used
// as a fallback for tests but the CLI always supplies the target directory.
type Options struct {
	SnapshotID string
	Connection string
	Format     Format
	WriteJSON  bool
	// Version is the dbx build identifier stamped into the sidecar. It is
	// metadata only and never read from the snapshot itself.
	Version string
	// Now overrides time.Now for deterministic tests.
	Now time.Time
}

// Result reports what was written.
type Result struct {
	DataPath  string
	Sidecar   string // empty when --no-json
	RowCount  int
	Columns   []string
	Format    Format
	ExportedAt time.Time
}

// Write renders snapshot data to outPath in opts.Format, then optionally
// writes the JSON sidecar next to it. When opts.WriteJSON is true the
// sidecar is written first, then the data file, so a partial state never
// claims a row count the data file does not yet contain.
func Write(outPath string, data json.RawMessage, opts Options) (Result, error) {
	if err := validate(opts); err != nil {
		return Result{}, err
	}

	rows, columns, err := decodeRows(data)
	if err != nil {
		return Result{}, err
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	res := Result{
		DataPath:   outPath,
		RowCount:   len(rows),
		Columns:    columns,
		Format:     opts.Format,
		ExportedAt: now,
	}

	var body []byte
	switch opts.Format {
	case FormatCSV:
		body, err = renderCSV(columns, rows)
	case FormatJSONL:
		body, err = renderJSONL(rows)
	default:
		return Result{}, fmt.Errorf("unsupported export format: %s", opts.Format)
	}
	if err != nil {
		return Result{}, err
	}

	if opts.WriteJSON {
		sidecar, err := sidecarPath(outPath)
		if err != nil {
			return Result{}, err
		}
		sidecarBody, err := RenderSidecar(Sidecar{
			Version:    SidecarVersion,
			Kind:       KindSnapshotExport,
			SnapshotID: opts.SnapshotID,
			Connection: opts.Connection,
			ExportedAt: res.ExportedAt,
			RowCount:   res.RowCount,
			Columns:    res.Columns,
			Format:     string(res.Format),
			DBXVersion: opts.Version,
		})
		if err != nil {
			return Result{}, err
		}
		if err := AtomicWrite(sidecar, sidecarBody, 0o600); err != nil {
			return Result{}, fmt.Errorf("write sidecar: %w", err)
		}
		res.Sidecar = sidecar
	}

	if err := AtomicWrite(outPath, body, 0o600); err != nil {
		// Best-effort sidecar cleanup when data write fails after sidecar
		// already landed. The pair is broken either way; remove the
		// misleading sidecar so the user does not trust it.
		if res.Sidecar != "" {
			_ = os.Remove(res.Sidecar)
			res.Sidecar = ""
		}
		return Result{}, fmt.Errorf("write %s: %w", filepath.Base(outPath), err)
	}
	return res, nil
}

// ValidateFormat parses a CLI format string into a known Format.
func ValidateFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(FormatCSV):
		return FormatCSV, nil
	case string(FormatJSONL):
		return FormatJSONL, nil
	default:
		return "", fmt.Errorf("unsupported --format %q (want csv or jsonl)", s)
	}
}

// DefaultPath returns the conventional <snapshot-id>.<ext> next to outDir.
// When outDir is empty, it returns the bare filename (caller's cwd is
// expected to have been resolved already).
func DefaultPath(snapshotID string, format Format, outDir string) (string, error) {
	if strings.TrimSpace(snapshotID) == "" {
		return "", fmt.Errorf("snapshot id is required")
	}
	name := strings.TrimSpace(snapshotID) + "." + string(format)
	if outDir == "" {
		return filepath.Clean(name), nil
	}
	return filepath.Join(outDir, name), nil
}

func validate(opts Options) error {
	switch opts.Format {
	case FormatCSV, FormatJSONL:
	default:
		return fmt.Errorf("unsupported export format: %s", opts.Format)
	}
	if strings.TrimSpace(opts.SnapshotID) == "" {
		return fmt.Errorf("snapshot id is required")
	}
	return nil
}

// decodeRows turns the snapshot payload into a stable (rows, columns) pair.
//
//   - Array of objects: rows = each object; columns = sorted union of keys.
//   - Object: single row; columns = sorted keys.
//   - Array of scalars / primitives: one row, one column named "value".
//
// Anything else returns an error so the user gets a clear signal instead of
// a silently mangled file.
func decodeRows(raw json.RawMessage) ([]map[string]any, []string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil, fmt.Errorf("snapshot has no data to export")
	}

	switch trimmed[0] {
	case '[':
		var arr []any
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, nil, fmt.Errorf("snapshot data is not a valid JSON array: %w", err)
		}
		return rowsFromArray(arr)
	case '{':
		var obj map[string]any
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return nil, nil, fmt.Errorf("snapshot data is not a valid JSON object: %w", err)
		}
		columns := sortedKeys(obj)
		return []map[string]any{obj}, columns, nil
	default:
		// Scalar top-level → single column "value", one row.
		var scalar any
		if err := json.Unmarshal(trimmed, &scalar); err != nil {
			return nil, nil, fmt.Errorf("snapshot data is not valid JSON: %w", err)
		}
		return []map[string]any{{"value": scalar}}, []string{"value"}, nil
	}
}

func rowsFromArray(arr []any) ([]map[string]any, []string, error) {
	rows := make([]map[string]any, 0, len(arr))
	colSet := map[string]struct{}{}
	for i, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("snapshot row %d is not a JSON object", i)
		}
		for k := range obj {
			colSet[k] = struct{}{}
		}
		rows = append(rows, obj)
	}
	columns := make([]string, 0, len(colSet))
	for k := range colSet {
		columns = append(columns, k)
	}
	sort.Strings(columns)
	return rows, columns, nil
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// renderCSV writes the CSV body (RFC 4180 via encoding/csv).
func renderCSV(columns []string, rows []map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(columns); err != nil {
		return nil, fmt.Errorf("write csv header: %w", err)
	}
	for _, row := range rows {
		record := make([]string, len(columns))
		for i, col := range columns {
			record[i] = cellToCSV(row[col])
		}
		if err := w.Write(record); err != nil {
			return nil, fmt.Errorf("write csv row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("flush csv: %w", err)
	}
	return buf.Bytes(), nil
}

// cellToCSV renders a single JSON cell as a CSV string. nil → empty.
// Numbers and booleans keep their JSON form. Strings pass through as-is;
// the csv writer handles quoting.
func cellToCSV(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		// JSON numbers decode to float64; preserve integer form when possible
		// so columns like ids do not show up as "1.000000".
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case json.Number:
		return x.String()
	case map[string]any, []any:
		b, err := json.Marshal(x)
		if err != nil {
			return ""
		}
		return string(b)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// renderJSONL writes one JSON object per line, terminated by '\n'.
//
// encoding/json's Marshal escapes '<', '>', and '&' so the output is safe
// for HTML embedding; we deliberately accept that minor cost because the
// alternative (a hand-rolled encoder) invites subtle bugs.
func renderJSONL(rows []map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	for i, row := range rows {
		b, err := json.Marshal(row)
		if err != nil {
			return nil, fmt.Errorf("marshal jsonl row %d: %w", i, err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// Sidecar captures the audit metadata the plan requires. Field order matches
// the README example so a quick `cat` is informative.
//
// Kind distinguishes sibling uses of the sidecar envelope ("snapshot_export"
// for `dbx export`, "explain" for `dbx explain --json`). The field is
// additive; older readers ignore unknown keys. Plan 009 (EXPLAIN
// pretty-printer) reuses this struct directly via RenderSidecar so the
// metadata shape stays uniform across commands.
type Sidecar struct {
	Version    string    `json:"version"`
	Kind       string    `json:"kind,omitempty"`
	SnapshotID string    `json:"snapshot_id"`
	Connection string    `json:"connection,omitempty"`
	ExportedAt time.Time `json:"exported_at"`
	RowCount   int       `json:"row_count"`
	Columns    []string  `json:"columns"`
	Format     string    `json:"format"`
	DBXVersion string    `json:"dbx_version,omitempty"`
}

// Known Kind values for the Sidecar envelope. Empty/missing is treated as
// "snapshot_export" for backward compatibility with Plan 008 readers.
const (
	KindSnapshotExport = "snapshot_export"
	KindExplain        = "explain"
)

// RenderSidecar encodes a Sidecar as pretty-printed JSON (2-space indent +
// trailing newline). Exposed so other commands can stamp the same audit
// shape without duplicating the formatter (Plan 009 EXPLAIN pretty-printer
// reuses this directly).
func RenderSidecar(s Sidecar) ([]byte, error) {
	if s.Version == "" {
		s.Version = SidecarVersion
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// sidecarPath mirrors outPath with .json appended (or .json.json when the
// data file is already .json). We always emit "<data>.meta.json" so the pair
// sorts together in directory listings.
func sidecarPath(outPath string) (string, error) {
	if strings.TrimSpace(outPath) == "" {
		return "", fmt.Errorf("output path is required")
	}
	return outPath + ".meta.json", nil
}

// AtomicWrite writes data to path via a temp file in the same directory,
// fsyncs it, then renames it into place. Same-directory tempfile guarantees
// rename(2) is atomic on POSIX; the fsync ensures the bytes (and the
// directory entry, after rename) hit stable storage.
//
// This is a hardened copy of snapshot.atomicWrite that adds fsync. Keeping
// the helper in this package avoids changing the snapshot contract and
// keeps the export concern isolated. Exposed so other commands (Plan 009
// `dbx explain --json`) can reuse the same write barrier without
// duplicating the dance.
func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".dbx-export-tmp-*")
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename export: %w", err)
	}
	// fsync the directory so the rename is durable across crashes.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	success = true
	return nil
}

