// Package explain runs EXPLAIN against a database connection and renders
// the result in two modes: a human-readable table (default) and a raw JSON
// blob for machine consumption. Sidecar metadata follows the same audit
// envelope shape as `internal/export` (Plan 008) so the two commands
// share a single observable format.
//
// Design notes (Plan 009):
//
//   - One package, two consumers: `cmd/dbx` (CLI) and `lua/dbx` (Neovim).
//     Both call into Run / RenderTabular / RenderJSON. The CLI is the only
//     place that opens a real database connection; tests inject fakes via
//     db.DB so the rendering + dispatch path stays offline-green.
//   - Tabular output is column-driven, not row-driven. The canonical
//     columns (id, select_type, table, type, possible_keys, key, key_len,
//     ref, rows, Extra) match MySQL 5.7+ EXPLAIN output. Missing columns
//     render as empty cells; extra server-side columns (partitions,
//     filtered) are dropped silently because the plan locks the schema.
//   - JSON mode emits the raw `EXPLAIN FORMAT=JSON` response. MySQL folds
//     the entire plan into one column of one row; we surface that string
//     as-is so callers can parse with their own tooling.
//   - Sidecar generation reuses internal/export.RenderSidecar +
//     internal/export.AtomicWrite so Plan 008 invariants (atomic rename,
//     sidecar written before data, audit-only metadata) carry over.
package explain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jd4n14/dbx/internal/db"
)

// Mode selects between human-readable tabular and machine JSON output.
type Mode string

// Supported explain modes.
const (
	ModeTabular Mode = "tabular"
	ModeJSON    Mode = "json"
)

// DefaultExtraTruncate is the column-width cap applied to the Extra cell
// when rendering tabular output. Anything longer is suffixed with "…". The
// value is a generous default; tests override it to lock behaviour.
const DefaultExtraTruncate = 80

// CanonicalColumns is the locked tabular column order. Plan 009 SCOPE
// pins this list; missing server-side columns render as empty cells and
// extra columns are dropped silently. Keeping the slice as a package-level
// constant means both Run (filtering) and RenderTabular (header) share a
// single source of truth.
var CanonicalColumns = []string{
	"id", "select_type", "table", "type", "possible_keys",
	"key", "key_len", "ref", "rows", "Extra",
}

// Result captures the explain plan after a successful Run. Columns and
// Rows describe tabular mode; RawJSON carries the untouched server response
// in JSON mode. Empty RawJSON in tabular mode (and vice-versa) is
// intentional — each mode has exactly one useful field.
type Result struct {
	Mode    Mode
	Columns []string   // tabular: canonical columns, server-supplied order otherwise
	Rows    [][]any    // tabular: row data, one slice per EXPLAIN row
	RawJSON []byte     // json: raw EXPLAIN FORMAT=JSON response bytes
}

// Run validates SQL against the read-only policy, opens the supplied
// database, executes EXPLAIN (or EXPLAIN FORMAT=JSON), and scans every row
// into a Result. The caller owns the database lifetime; Run only uses the
// db.DB passed in.
//
// ValidateQuery is the write barrier — same allowlist as `dbx query`.
// EXPLAIN sits inside that allowlist, so EXPLAIN cannot reach the server
// when ValidateQuery refuses the input. EXPLAIN ANALYZE etc. would not be
// blocked by the allowlist (none start with EXPLAIN <verb>); the package
// docs note the limit. Plan 004 danger preflight is intentionally NOT
// auto-routed here per the plan's "zero-cost" rule.
func Run(ctx context.Context, database db.DB, sqlText string, mode Mode) (Result, error) {
	sqlText = strings.TrimSpace(sqlText)
	if sqlText == "" {
		return Result{}, fmt.Errorf("explain: SQL is empty")
	}
	switch mode {
	case ModeTabular:
		return runTabular(ctx, database, sqlText)
	case ModeJSON:
		return runJSON(ctx, database, sqlText)
	default:
		return Result{}, fmt.Errorf("explain: unsupported mode %q", mode)
	}
}

// runTabular issues EXPLAIN <sql> and scans every row. Missing canonical
// columns render as nil; we don't refuse the call because older servers
// legitimately omit some fields.
func runTabular(ctx context.Context, database db.DB, sqlText string) (Result, error) {
	rows, err := database.QueryContext(ctx, "EXPLAIN "+sqlText)
	if err != nil {
		return Result{}, fmt.Errorf("explain: %w", err)
	}
	defer rows.Close()

	cols, values, err := scanAll(rows)
	if err != nil {
		return Result{}, err
	}

	filtered := filterCanonicalColumns(cols, values)
	return Result{
		Mode:    ModeTabular,
		Columns: append([]string(nil), CanonicalColumns...),
		Rows:    filtered,
	}, nil
}

// runJSON issues EXPLAIN FORMAT=JSON <sql>. MySQL folds the whole plan
// into a single column; we surface that column's first row as raw bytes
// so callers can json.Decode the canonical MySQL EXPLAIN envelope.
func runJSON(ctx context.Context, database db.DB, sqlText string) (Result, error) {
	rows, err := database.QueryContext(ctx, "EXPLAIN FORMAT=JSON "+sqlText)
	if err != nil {
		return Result{}, fmt.Errorf("explain json: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return Result{}, fmt.Errorf("explain json: columns: %w", err)
	}
	if len(cols) == 0 {
		return Result{}, fmt.Errorf("explain json: empty column list from server")
	}

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Result{}, fmt.Errorf("explain json: %w", err)
		}
		return Result{}, fmt.Errorf("explain json: no rows in FORMAT=JSON response")
	}

	// MySQL EXPLAIN FORMAT=JSON always returns exactly one column (the
	// JSON-encoded plan) in exactly one row. We still accept any number
	// of columns and grab the first cell — falling back to the others
	// would invite ambiguous results.
	holders := make([]any, len(cols))
	dest := make([]any, len(cols))
	for i := range holders {
		dest[i] = &holders[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return Result{}, fmt.Errorf("explain json: scan: %w", err)
	}
	if rows.Next() {
		return Result{}, fmt.Errorf("explain json: unexpected extra row in FORMAT=JSON response")
	}
	if err := rows.Err(); err != nil {
		return Result{}, fmt.Errorf("explain json: %w", err)
	}

	raw, err := cellJSON(holders[0])
	if err != nil {
		return Result{}, fmt.Errorf("explain json: %w", err)
	}
	if !json.Valid(raw) {
		return Result{}, fmt.Errorf("explain json: server response is not valid JSON")
	}
	// Pretty-print so on-disk JSON is human-inspectable (matches the rest
	// of dbx's stdout contract: 2-space indent + trailing newline).
	pretty, err := indentJSON(raw)
	if err != nil {
		return Result{}, err
	}
	return Result{Mode: ModeJSON, RawJSON: pretty}, nil
}

// filterCanonicalColumns keeps the canonical column order even when the
// server returns a different shape. Extra columns are dropped; missing
// canonical columns become nil. The mapping is stable: index i of the
// returned row corresponds to CanonicalColumns[i]. Column name lookups
// are case-insensitive because MySQL's "Extra" header is mixed case while
// every other canonical column is lowercase.
func filterCanonicalColumns(cols []string, rows [][]any) [][]any {
	idx := make(map[string]int, len(cols))
	for i, c := range cols {
		idx[strings.ToLower(c)] = i
	}
	out := make([][]any, len(rows))
	for r, row := range rows {
		filtered := make([]any, len(CanonicalColumns))
		for i, name := range CanonicalColumns {
			if src, ok := idx[strings.ToLower(name)]; ok && src < len(row) {
				filtered[i] = row[src]
			}
		}
		out[r] = filtered
	}
	return out
}

// scanAll mirrors internal/query.scanAll but lives here to avoid coupling
// the explain package to the query package (and its policy machinery).
// Both packages happen to scan the same db.Rows interface; the duplication
// is one short function and keeps each package independently testable.
func scanAll(rows db.Rows) (columns []string, values [][]any, err error) {
	columns, err = rows.Columns()
	if err != nil {
		return nil, nil, fmt.Errorf("scan: columns: %w", err)
	}
	n := len(columns)
	for rows.Next() {
		holders := make([]any, n)
		dest := make([]any, n)
		for i := range holders {
			dest[i] = &holders[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, fmt.Errorf("scan: %w", err)
		}
		row := make([]any, n)
		copy(row, holders)
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("scan: %w", err)
	}
	return columns, values, nil
}

// cellJSON returns the cell value as a JSON-encoded byte slice. The MySQL
// driver usually hands EXPLAIN FORMAT=JSON over as a []byte (the JSON
// string itself), but tests + other drivers may return string. Anything
// else is rejected so we never embed arbitrary Go values into the
// "raw EXPLAIN FORMAT=JSON response" output.
func cellJSON(v any) ([]byte, error) {
	switch t := v.(type) {
	case nil:
		return nil, fmt.Errorf("EXPLAIN FORMAT=JSON returned NULL")
	case []byte:
		if len(t) == 0 {
			return nil, fmt.Errorf("EXPLAIN FORMAT=JSON returned empty bytes")
		}
		return t, nil
	case string:
		if t == "" {
			return nil, fmt.Errorf("EXPLAIN FORMAT=JSON returned empty string")
		}
		return []byte(t), nil
	default:
		return nil, fmt.Errorf("EXPLAIN FORMAT=JSON cell has unexpected type %T", v)
	}
}

// indentJSON pretty-prints JSON with 2-space indent + trailing newline.
// Reused across the codebase so the stdout contract stays uniform.
func indentJSON(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return nil, fmt.Errorf("pretty json: %w", err)
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// RenderTabular turns a tabular Result into a fixed-column ASCII table.
//
//   - Column widths are the max of every cell's rune length.
//   - NULL renders as the empty string (no "NULL" marker — the table is
//     for human eyes, not DataGrip-style fidelity).
//   - The Extra column truncates with "…" at `truncateAt` runes when set
//     (use DefaultExtraTruncate for the plan-locked default; pass 0 to
//     disable truncation, mainly for tests).
//   - Output ends with a trailing newline so it concatenates cleanly with
//     other stdout streams.
//
// Pure: no IO, no time. Safe to call from tests without setup.
func RenderTabular(r Result, truncateAt int) ([]byte, error) {
	if r.Mode != ModeTabular {
		return nil, fmt.Errorf("RenderTabular: result is not tabular (mode=%s)", r.Mode)
	}
	if truncateAt < 0 {
		return nil, fmt.Errorf("RenderTabular: truncateAt must be >= 0")
	}
	cols := r.Columns
	if len(cols) == 0 || len(r.Rows) == 0 {
		// Empty plan: emit a single newline so callers always see
		// newline-terminated stdout.
		return []byte("\n"), nil
	}

	// Build header + row cells as rune counts.
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = runeLen(c)
	}

	cellStrs := make([][]string, len(r.Rows))
	for ri, row := range r.Rows {
		cellStrs[ri] = make([]string, len(cols))
		for ci := range cols {
			s := formatCell(row[ci])
			if ci == indexOf(cols, "Extra") && truncateAt > 0 && runeLen(s) > truncateAt {
				s = truncateRunes(s, truncateAt) + "…"
			}
			cellStrs[ri][ci] = s
			if w := runeLen(s); w > widths[ci] {
				widths[ci] = w
			}
		}
	}

	var b strings.Builder
	writeRow(&b, cols, widths)
	writeSep(&b, widths)
	for _, row := range cellStrs {
		writeRow(&b, row, widths)
	}
	b.WriteByte('\n')
	return []byte(b.String()), nil
}

// indexOf returns the position of name in cols, or -1. Used to special-
// case the Extra column truncation. Linear scan is fine — the canonical
// column count is 10.
func indexOf(cols []string, name string) int {
	for i, c := range cols {
		if strings.EqualFold(c, name) {
			return i
		}
	}
	return -1
}

// writeRow renders one row of `cells` left-padded to widths[i], with two
// spaces between columns and no trailing padding on the last cell.
func writeRow(b *strings.Builder, cells []string, widths []int) {
	for i, c := range cells {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(c)
		if i < len(cells)-1 {
			pad := widths[i] - runeLen(c)
			for pad > 0 {
				b.WriteByte(' ')
				pad--
			}
		}
	}
	b.WriteByte('\n')
}

// writeSep renders the header separator line (---  ----  ...).
func writeSep(b *strings.Builder, widths []int) {
	for i, w := range widths {
		if i > 0 {
			b.WriteString("  ")
		}
		for j := 0; j < w; j++ {
			b.WriteByte('-')
		}
	}
	b.WriteByte('\n')
}

// formatCell renders a single tabular cell. nil → empty. []byte → string
// (matches jsonutil behaviour for the EXPLAIN columns MySQL emits). Other
// types fall through to fmt.Sprintf("%v", v) which yields a stable
// representation for ints, strings, and time.Time (via its Stringer).
func formatCell(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// runeLen is utf8.RuneCountInString without importing unicode/utf8 in
// every caller — keeps the file self-contained and the table printer
// avoids an extra dep.
func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// truncateRunes keeps the first n runes of s. Equivalent to slicing on
// the byte offset of the (n-1)th rune boundary.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// SidecarKey derives a stable identifier for the explain sidecar's
// snapshot_id field. We use a sortable timestamp + the first 32 chars of
// the connection alias so two explains against the same conn at distinct
// times are easy to tell apart while keeping the audit envelope uniform.
// The plan pins the field name; this function is the explanation of how
// "no query text, no connection secrets" is preserved while still
// producing a useful label.
func SidecarKey(now time.Time, connection string) string {
	ts := now.UTC().Format("20060102T150405Z")
	alias := strings.TrimSpace(connection)
	if alias == "" {
		return "explain-" + ts
	}
	// Identifiers stay filename-safe; reject anything outside the safe
	// snapshot-name alphabet so the sidecar file name never has to escape
	// a quote or a slash.
	clean := make([]rune, 0, len(alias))
	for _, r := range alias {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			clean = append(clean, r)
		default:
			clean = append(clean, '_')
		}
	}
	if len(clean) > 32 {
		clean = clean[:32]
	}
	return "explain-" + ts + "-" + string(clean)
}

