package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jd4n14/dbx/internal/history"
)

// runHistory dispatches the history subcommand.
func runHistory(args []string) error {
	return runHistoryCmd(args, os.Stdin, os.Stdout, os.Stderr, "", true)
}

// runHistoryCmd is the testable entry for dbx history. cwd defaults to
// os.Getwd() when empty.
func runHistoryCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string, _ bool) error {
	_ = stdin
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}
	}
	if len(args) == 0 {
		_ = printHistoryUsage(stderr)
		return fmt.Errorf("history usage: dbx history <list|show|run|clear> [args]")
	}
	switch args[0] {
	case "list":
		return runHistoryList(args[1:], stdout, stderr, cwd)
	case "show":
		return runHistoryShow(args[1:], stdout, stderr, cwd)
	case "run":
		// run is implemented client-side: it prints the SQL so callers can
		// pipe it back into dbx query (or invoke :DbHistoryLast from Vim).
		return runHistoryShow(args[1:], stdout, stderr, cwd)
	case "clear":
		return runHistoryClear(args[1:], stderr, cwd)
	case "help", "--help", "-h":
		_ = printHistoryUsage(stderr)
		return nil
	default:
		_ = printHistoryUsage(stderr)
		return fmt.Errorf("unknown history subcommand: %s", args[0])
	}
}

func printHistoryUsage(w io.Writer) error {
	_, err := fmt.Fprintf(w, `Usage: dbx history <command> [flags]

Commands:
  list             Print recent history entries (newest first)
  show <index>     Print the SQL of a single entry (1-based, 1 = newest)
  run <index>      Like show (prints SQL; safe to pipe back into dbx query)
  clear            Delete the history file

Flags (list):
  --limit N        Max entries to print (default 50; <=0 means history.DefaultLimit)
  --json           Emit JSON Lines instead of tab-separated columns

Flags (show / run):
  --json           Emit the full record (index/ts/connection/sql) as JSON
                   instead of just the SQL text

Index 1 is the newest entry.
`)
	return err
}

func runHistoryList(args []string, stdout, stderr io.Writer, cwd string) error {
	fs := flag.NewFlagSet("history list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("limit", 50, "max entries to print (<=0 uses history.DefaultLimit)")
	asJSON := fs.Bool("json", false, "emit JSON Lines instead of tab-separated columns")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit <= 0 {
		*limit = history.DefaultLimit
	}
	entries, err := history.List(cwd, *limit)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	w := bufio.NewWriter(stdout)
	defer w.Flush()
	for _, ent := range entries {
		if *asJSON {
			body, err := marshalListedEntry(ent)
			if err != nil {
				return fmt.Errorf("encode history entry: %w", err)
			}
			if _, err := w.Write(body); err != nil {
				return err
			}
			if err := w.WriteByte('\n'); err != nil {
				return err
			}
			continue
		}
		ts := ent.Timestamp.UTC().Format(time.RFC3339)
		// Default tabular view: index\tts\tconnection\tsql
		// SQL may contain newlines/tabs (multi-statement scripts); flatten to
		// a single space so each entry is exactly one line.
		sql := flattenForTable(ent.SQL)
		if _, err := fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", ent.Index, ts, ent.Connection, sql); err != nil {
			return err
		}
	}
	return nil
}

func runHistoryShow(args []string, stdout, stderr io.Writer, cwd string) error {
	fs := flag.NewFlagSet("history show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit the full record (JSON) instead of just the SQL text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return fmt.Errorf("history show requires an index (usage: dbx history show <index>)")
	}
	idx, err := parseHistoryIndex(rest[0])
	if err != nil {
		return err
	}
	listed, err := history.List(cwd, 0)
	if err != nil {
		return err
	}
	if len(listed) == 0 {
		return fmt.Errorf("no history; run dbx query first")
	}
	if idx > len(listed) {
		return fmt.Errorf("history index %d out of range (have %d)", idx, len(listed))
	}
	target := listed[idx-1]
	if *asJSON {
		body, err := json.Marshal(struct {
			Index int `json:"index"`
			history.Entry
		}{Index: target.Index, Entry: target.Entry})
		if err != nil {
			return fmt.Errorf("encode history record: %w", err)
		}
		body = append(body, '\n')
		_, err = stdout.Write(body)
		return err
	}
	out := target.SQL
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	_, err = io.WriteString(stdout, out)
	return err
}

func runHistoryClear(args []string, stderr io.Writer, cwd string) error {
	fs := flag.NewFlagSet("history clear", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("history clear takes no arguments")
	}
	return history.Clear(cwd)
}

// flattenForTable replaces characters that would break the tab-separated
// default rendering so each history entry occupies exactly one line.
func flattenForTable(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return s
}

// parseHistoryIndex validates a 1-based positive integer from a CLI token.
// Rejects leading "-" (flag-like) and non-numeric input with a clear message.
func parseHistoryIndex(token string) (int, error) {
	if strings.HasPrefix(token, "-") {
		return 0, fmt.Errorf("history index must be a positive integer (got %q)", token)
	}
	idx, err := strconv.Atoi(token)
	if err != nil || idx <= 0 {
		return 0, fmt.Errorf("history index must be a positive integer (got %q)", token)
	}
	return idx, nil
}

// historyJSONEntry mirrors an on-disk Entry but flattens the Index column for
// JSON consumers (Vim + external tooling). Kept package-private; consumers
// see {"index", "ts", "connection", "sql", "rows", "bytes", "duration_ms"}.
type historyJSONEntry struct {
	Index      int       `json:"index"`
	Timestamp  time.Time `json:"ts"`
	Connection string    `json:"connection,omitempty"`
	SQL        string    `json:"sql"`
	Rows       int       `json:"rows"`
	Bytes      int       `json:"bytes"`
	DurationMs int64     `json:"duration_ms"`
}

func marshalListedEntry(ent history.ListedEntry) ([]byte, error) {
	return json.Marshal(historyJSONEntry{
		Index:      ent.Index,
		Timestamp:  ent.Timestamp,
		Connection: ent.Connection,
		SQL:        ent.SQL,
		Rows:       ent.Rows,
		Bytes:      ent.Bytes,
		DurationMs: ent.DurationMs,
	})
}
