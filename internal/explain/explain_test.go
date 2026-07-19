package explain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jd4n14/dbx/internal/db"
)

// fakeDB implements db.DB with a programmable QueryContext + Ping + Close.
// It mirrors the offline test seam in internal/query/run_test.go so we
// don't pull in a mock library.
type fakeDB struct {
	queryFn func(ctx context.Context, q string, args ...any) (db.Rows, error)
	lastSQL string
}

func (f *fakeDB) QueryContext(ctx context.Context, q string, args ...any) (db.Rows, error) {
	f.lastSQL = q
	if f.queryFn == nil {
		return nil, errors.New("fakeDB: unexpected QueryContext")
	}
	return f.queryFn(ctx, q, args...)
}

func (f *fakeDB) PingContext(ctx context.Context) error { return nil }

func (f *fakeDB) Close() error { return nil }

// fakeRows implements db.Rows over in-memory columnar data, same shape as
// the helper in internal/query/run_test.go.
type fakeRows struct {
	cols   []string
	data   [][]any
	i      int
	closed bool
	err    error
}

func (r *fakeRows) Columns() ([]string, error) {
	if r.closed {
		return nil, errors.New("fakeRows: closed")
	}
	return r.cols, nil
}

func (r *fakeRows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	if r.i >= len(r.data) {
		return false
	}
	r.i++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.closed {
		return errors.New("fakeRows: closed")
	}
	if r.i == 0 || r.i > len(r.data) {
		return errors.New("fakeRows: Scan without Next")
	}
	row := r.data[r.i-1]
	for i := range dest {
		p, ok := dest[i].(*any)
		if !ok {
			return fmt.Errorf("fakeRows: dest[%d] is %T, want *any", i, dest[i])
		}
		if i < len(row) {
			*p = row[i]
		} else {
			*p = nil
		}
	}
	return nil
}

func (r *fakeRows) Err() error { return r.err }

func (r *fakeRows) Close() error { r.closed = true; return nil }

// tabularServerRows returns rows mimicking a real MySQL EXPLAIN response.
// Server includes the canonical columns plus `partitions` and `filtered`
// to exercise the drop-extra-columns path.
func tabularServerRows() *fakeRows {
	return &fakeRows{
		cols: []string{
			"id", "select_type", "table", "partitions", "type",
			"possible_keys", "key", "key_len", "ref", "rows",
			"filtered", "Extra",
		},
		data: [][]any{
			{
				int64(1), "SIMPLE", "orders", nil, "ALL",
				nil, nil, nil, nil, int64(1234),
				nil, "Using where; Using temporary; Using filesort; this extra value is intentionally very long to exercise the truncate path",
			},
			{
				int64(1), "SIMPLE", "order_items", nil, "eq_ref",
				"PRIMARY", "PRIMARY", "8", "wms.orders.id", int64(1),
				nil, "Using where",
			},
		},
	}
}

func TestRun_TabularFiltersCanonicalColumns(t *testing.T) {
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, q string, args ...any) (db.Rows, error) {
			if !strings.HasPrefix(q, "EXPLAIN ") || strings.HasPrefix(q, "EXPLAIN FORMAT=JSON") {
				t.Fatalf("expected EXPLAIN prefix, got %q", q)
			}
			return tabularServerRows(), nil
		},
	}
	res, err := Run(context.Background(), fdb, "SELECT * FROM orders", ModeTabular)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Mode != ModeTabular {
		t.Fatalf("mode: %s", res.Mode)
	}
	if len(res.Columns) != len(CanonicalColumns) {
		t.Fatalf("columns: got %d want %d", len(res.Columns), len(CanonicalColumns))
	}
	for i, c := range res.Columns {
		if c != CanonicalColumns[i] {
			t.Fatalf("column %d: got %q want %q", i, c, CanonicalColumns[i])
		}
	}
	if len(res.Rows) != 2 {
		t.Fatalf("rows: %d", len(res.Rows))
	}
	if len(res.Rows[0]) != len(CanonicalColumns) {
		t.Fatalf("row[0] cell count: %d", len(res.Rows[0]))
	}
	// partitions/filtered must be dropped — indexOf("Extra") is the last
	// canonical column and must carry the Extra value from the server.
	if got := res.Rows[0][indexOf(res.Columns, "Extra")]; got == nil {
		t.Fatalf("row[0].Extra dropped (should be kept)")
	}
	if got := res.Rows[0][indexOf(res.Columns, "table")]; got != "orders" {
		t.Fatalf("row[0].table: %v", got)
	}
	if got := res.Rows[1][indexOf(res.Columns, "ref")]; got != "wms.orders.id" {
		t.Fatalf("row[1].ref: %v", got)
	}
}

func TestRun_TabularMissingServerColumnRendersAsNil(t *testing.T) {
	// A 5.6-style EXPLAIN returning fewer columns (no possible_keys / ref)
	// must still produce a 10-wide row with nil cells in the missing slots.
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, q string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"id", "select_type", "table", "type", "rows", "Extra"},
				data: [][]any{
					{int64(1), "SIMPLE", "t", "ALL", int64(10), nil},
				},
			}, nil
		},
	}
	res, err := Run(context.Background(), fdb, "SELECT * FROM t", ModeTabular)
	if err != nil {
		t.Fatal(err)
	}
	row := res.Rows[0]
	if row[indexOf(res.Columns, "possible_keys")] != nil {
		t.Fatalf("possible_keys should be nil, got %v", row[indexOf(res.Columns, "possible_keys")])
	}
	if row[indexOf(res.Columns, "ref")] != nil {
		t.Fatalf("ref should be nil")
	}
}

func TestRun_EmptySQLRejected(t *testing.T) {
	fdb := &fakeDB{}
	_, err := Run(context.Background(), fdb, "", ModeTabular)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty-SQL error, got %v", err)
	}
}

func TestRun_UnsupportedMode(t *testing.T) {
	fdb := &fakeDB{}
	_, err := Run(context.Background(), fdb, "SELECT 1", Mode("tree"))
	if err == nil {
		t.Fatal("expected unsupported-mode error")
	}
}

func TestRun_QueryErrorIsWrapped(t *testing.T) {
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, q string, args ...any) (db.Rows, error) {
			return nil, errors.New("syntax error near FROM")
		},
	}
	_, err := Run(context.Background(), fdb, "SELECT FROM", ModeTabular)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "explain:") {
		t.Fatalf("error must carry explain: prefix: %v", err)
	}
	if !strings.Contains(err.Error(), "syntax error near FROM") {
		t.Fatalf("error must preserve server message: %v", err)
	}
}

func TestRun_JSONReturnsPrettyRawBlob(t *testing.T) {
	rawJSON := []byte(`{"query_block":{"select_id":1,"table":{"table_name":"orders","access_type":"ALL","rows_examined_per_scan":1234,"filtered":100.0,"attached_condition":"orders.status = 'pending'"}}}`)
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, q string, args ...any) (db.Rows, error) {
			if !strings.HasPrefix(q, "EXPLAIN FORMAT=JSON ") {
				t.Fatalf("expected FORMAT=JSON prefix, got %q", q)
			}
			return &fakeRows{
				cols: []string{"EXPLAIN"},
				data: [][]any{{rawJSON}},
			}, nil
		},
	}
	res, err := Run(context.Background(), fdb, "SELECT * FROM orders", ModeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeJSON {
		t.Fatalf("mode: %s", res.Mode)
	}
	if len(res.RawJSON) == 0 {
		t.Fatal("empty RawJSON")
	}
	// Round-trip: the pretty body must still be valid JSON equal to the
	// canonical MySQL EXPLAIN FORMAT=JSON envelope.
	var decoded map[string]any
	if err := json.Unmarshal(res.RawJSON, &decoded); err != nil {
		t.Fatalf("raw output is not valid JSON: %v\n%s", err, res.RawJSON)
	}
	if _, ok := decoded["query_block"]; !ok {
		t.Fatalf("missing query_block: %s", res.RawJSON)
	}
	// Pretty contract: 2-space indent present somewhere in the body.
	if !strings.Contains(string(res.RawJSON), "\n  ") {
		t.Fatalf("expected pretty-printed JSON, got %q", res.RawJSON)
	}
}

func TestRun_JSONRejectsServerNull(t *testing.T) {
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, q string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"EXPLAIN"},
				data: [][]any{nil},
			}, nil
		},
	}
	_, err := Run(context.Background(), fdb, "SELECT 1", ModeJSON)
	if err == nil || !strings.Contains(err.Error(), "NULL") {
		t.Fatalf("expected NULL rejection, got %v", err)
	}
}

func TestRun_JSONRejectsInvalidServerJSON(t *testing.T) {
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, q string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"EXPLAIN"},
				data: [][]any{{[]byte("not json")}},
			}, nil
		},
	}
	_, err := Run(context.Background(), fdb, "SELECT 1", ModeJSON)
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("expected invalid-json rejection, got %v", err)
	}
}

func TestRenderTabular_HeaderAndAlignment(t *testing.T) {
	res := Result{
		Mode: ModeTabular,
		Columns: []string{
			"id", "select_type", "table", "type", "possible_keys",
			"key", "key_len", "ref", "rows", "Extra",
		},
		Rows: [][]any{
			{int64(1), "SIMPLE", "orders", "ALL", nil, nil, nil, nil, int64(1234), "Using where"},
			{int64(1), "SIMPLE", "order_items", "eq_ref", "PRIMARY", "PRIMARY", "8", "wms.orders.id", int64(1), "Using where"},
		},
	}
	out, err := RenderTabular(res, 0)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want header + sep + 2 rows = 4 lines, got %d: %q", len(lines), out)
	}
	// Parse header by column-start positions: each cell is left-aligned
	// within its column width and cells are separated by exactly two
	// spaces. We compute column widths from the separator line and slice
	// the header accordingly.
	colWidths := parseSeparator(lines[1])
	header := parseRow(lines[0], colWidths)
	want := []string{"id", "select_type", "table", "type", "possible_keys", "key", "key_len", "ref", "rows", "Extra"}
	if len(header) != len(want) {
		t.Fatalf("header cols %d: %q", len(header), header)
	}
	for i, h := range want {
		if header[i] != h {
			t.Fatalf("header[%d]=%q want %q", i, header[i], h)
		}
	}
	if !strings.HasPrefix(lines[1], "--  ") {
		t.Fatalf("separator must start with 2 dashes + 2 spaces: %q", lines[1])
	}
	if !strings.Contains(lines[2], "Using where") {
		t.Fatalf("row 1 should contain Extra value: %q", lines[2])
	}
	if strings.Contains(lines[2], "Using where; Using temporary") {
		t.Fatalf("Extra should not be truncated when truncateAt=0: %q", lines[2])
	}
}

// parseSeparator extracts column widths from the "---  ----  ..." line.
// Each dash run maps to a column; the two-space gap is the separator.
func parseSeparator(sep string) []int {
	widths := []int{}
	run := 0
	for _, ch := range sep {
		if ch == '-' {
			run++
			continue
		}
		if run > 0 {
			widths = append(widths, run)
			run = 0
		}
	}
	if run > 0 {
		widths = append(widths, run)
	}
	return widths
}

// parseRow slices a row into cells given the column widths (in runes).
// Cells are left-aligned; the inter-cell separator is "  " (two spaces).
// We trim the trailing padding of each cell so callers can compare
// exactly. Rune-aware so multi-byte cells (e.g. the truncation ellipsis)
// survive intact.
func parseRow(line string, widths []int) []string {
	cells := make([]string, 0, len(widths))
	runes := []rune(line)
	cursor := 0
	for i, w := range widths {
		if cursor >= len(runes) {
			cells = append(cells, "")
			continue
		}
		end := cursor + w
		if end > len(runes) {
			end = len(runes)
		}
		cells = append(cells, strings.TrimRight(string(runes[cursor:end]), " "))
		cursor = end
		if i < len(widths)-1 {
			// Inter-cell separator is exactly two spaces.
			cursor += 2
		}
	}
	return cells
}

func TestRenderTabular_NULLRendersAsEmptyCell(t *testing.T) {
	res := Result{
		Mode: ModeTabular,
		Columns: CanonicalColumns,
		Rows: [][]any{
			{int64(1), "PRIMARY", nil, nil, nil, nil, nil, nil, nil, nil},
		},
	}
	out, err := RenderTabular(res, 0)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines: %d", len(lines))
	}
	colWidths := parseSeparator(lines[1])
	cells := parseRow(lines[2], colWidths)
	if len(cells) != len(CanonicalColumns) {
		t.Fatalf("cells: %d", len(cells))
	}
	// Columns 3..9 are NULL in the input — they must render as "".
	for i, name := range CanonicalColumns {
		if i < 2 {
			continue
		}
		if cells[i] != "" {
			t.Fatalf("%s should render as empty cell, got %q", name, cells[i])
		}
	}
}

func TestRenderTabular_ExtraTruncatesWithEllipsis(t *testing.T) {
	longExtra := strings.Repeat("a", 200)
	res := Result{
		Mode: ModeTabular,
		Columns: CanonicalColumns,
		Rows: [][]any{
			{int64(1), "SIMPLE", "t", "ALL", nil, nil, nil, nil, int64(1), longExtra},
		},
	}
	out, err := RenderTabular(res, 80)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "…") {
		t.Fatalf("truncated output must include ellipsis: %q", out)
	}
	// Slice the trailing column from the data row to inspect the cell
	// itself (not the padded surrounding whitespace).
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	row := lines[2]
	colWidths := parseSeparator(lines[1])
	cells := parseRow(row, colWidths)
	extra := cells[len(cells)-1]
	if runeLen(extra) > 81 {
		t.Fatalf("Extra should be <= 81 runes after truncation, got %d: %q", runeLen(extra), extra)
	}
	if !strings.HasSuffix(extra, "…") {
		t.Fatalf("Extra must end with ellipsis: %q", extra)
	}
	if !strings.HasPrefix(extra, strings.Repeat("a", 80)) {
		t.Fatalf("Extra must keep the first 80 a-runes: %q", extra)
	}
}

func TestRenderTabular_DefaultTruncationMatchesPlan(t *testing.T) {
	longExtra := strings.Repeat("z", 200)
	res := Result{
		Mode: ModeTabular,
		Columns: CanonicalColumns,
		Rows: [][]any{
			{int64(1), "SIMPLE", "t", "ALL", nil, nil, nil, nil, int64(1), longExtra},
		},
	}
	out, err := RenderTabular(res, DefaultExtraTruncate)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "…") {
		t.Fatalf("default truncation must include ellipsis: %q", out)
	}
}

func TestRenderTabular_NoRowsEmitsEmptyPlan(t *testing.T) {
	res := Result{Mode: ModeTabular, Columns: CanonicalColumns}
	out, err := RenderTabular(res, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "\n" {
		t.Fatalf("empty plan must emit a single newline, got %q", out)
	}
}

func TestRenderTabular_NoColumnsEmitsEmptyPlan(t *testing.T) {
	res := Result{Mode: ModeTabular}
	out, err := RenderTabular(res, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "\n" {
		t.Fatalf("missing columns must emit a single newline, got %q", out)
	}
}

func TestRenderTabular_WrongMode(t *testing.T) {
	_, err := RenderTabular(Result{Mode: ModeJSON}, 0)
	if err == nil || !strings.Contains(err.Error(), "not tabular") {
		t.Fatalf("expected mode error, got %v", err)
	}
}

func TestRenderTabular_NegativeTruncateRejected(t *testing.T) {
	res := Result{Mode: ModeTabular, Columns: CanonicalColumns, Rows: [][]any{{1, "SIMPLE", "t", "ALL", nil, nil, nil, nil, 1, ""}}}
	if _, err := RenderTabular(res, -1); err == nil {
		t.Fatal("expected error for negative truncateAt")
	}
}

func TestSidecarKey_Stable(t *testing.T) {
	ts := time.Date(2026, 7, 19, 17, 0, 0, 0, time.UTC)
	a := SidecarKey(ts, "local_wms")
	b := SidecarKey(ts, "local_wms")
	if a != b {
		t.Fatalf("SidecarKey must be deterministic: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "explain-20260719T170000Z-") {
		t.Fatalf("SidecarKey prefix: %q", a)
	}
	if strings.Contains(a, " ") || strings.Contains(a, "/") {
		t.Fatalf("SidecarKey must be filename-safe: %q", a)
	}
}

func TestSidecarKey_ConnectionAliasScrubbed(t *testing.T) {
	ts := time.Date(2026, 7, 19, 17, 0, 0, 0, time.UTC)
	got := SidecarKey(ts, "weird/alias with spaces & ; rm -rf /")
	for _, ch := range []rune{' ', '/', ';', '&'} {
		if strings.ContainsRune(got, ch) {
			t.Fatalf("SidecarKey leaked forbidden char %q: %q", ch, got)
		}
	}
}

func TestSidecarKey_NoConnectionStillDistinct(t *testing.T) {
	a := SidecarKey(time.Date(2026, 7, 19, 17, 0, 0, 0, time.UTC), "")
	b := SidecarKey(time.Date(2026, 7, 19, 17, 0, 1, 0, time.UTC), "")
	if a == b {
		t.Fatalf("timestamp must differentiate: %q", a)
	}
}

func TestTruncateRunes_BoundarySafe(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 0, ""},
		{"hello", 1, "h"},
		{"hello", 5, "hello"},
		{"hello", 6, "hello"},
		{"héllo", 3, "hél"},
	}
	for _, tc := range cases {
		got := truncateRunes(tc.in, tc.n)
		if got != tc.want {
			t.Fatalf("truncateRunes(%q, %d)=%q want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestFormatCell_Cases(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"hello", "hello"},
		{[]byte("bytes-here"), "bytes-here"},
		{int64(42), "42"},
	}
	for _, tc := range cases {
		if got := formatCell(tc.in); got != tc.want {
			t.Fatalf("formatCell(%v)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestFilterCanonicalColumns_DropsAndKeeps(t *testing.T) {
	cols := []string{"id", "select_type", "table", "type", "rows", "Extra", "partitions", "filtered"}
	rows := [][]any{
		{int64(1), "SIMPLE", "t", "ALL", int64(10), "x", nil, 50.0},
	}
	filtered := filterCanonicalColumns(cols, rows)
	if len(filtered) != 1 {
		t.Fatalf("rows: %d", len(filtered))
	}
	if len(filtered[0]) != len(CanonicalColumns) {
		t.Fatalf("row width: %d", len(filtered[0]))
	}
	if got := filtered[0][indexOf(CanonicalColumns, "Extra")]; got != "x" {
		t.Fatalf("Extra: %v", got)
	}
	// partitions / filtered must not appear anywhere in the row.
	for ci, name := range CanonicalColumns {
		if name == "partitions" || name == "filtered" {
			t.Fatalf("canonical list must not contain dropped columns; found %q at %d", name, ci)
		}
	}
}