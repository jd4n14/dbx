package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jd4n14/dbx/internal/snapshot"
)

// runSnapshot dispatches save (default), list, or show.
func runSnapshot(args []string) error {
	return runSnapshotCmd(args, os.Stdin, os.Stdout, os.Stderr, "", true)
}

// runSnapshotCmd is the testable entry for dbx snapshot.
//
// cwd defaults to os.Getwd when empty.
// useStdinAsPipe: when true and stdin implements *os.File that is not a char
// device, treat as piped JSON. When false, never read stdin for save (use last).
// Tests pass a non-empty Reader with useStdinAsPipe=true via the forcePipe path:
// if stdin is not *os.File, useStdinAsPipe decides whether to consume it.
func runSnapshotCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string, useStdinAsPipe bool) error {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}
	}

	if len(args) > 0 {
		switch args[0] {
		case "list":
			return runSnapshotList(args[1:], stdout, stderr, cwd)
		case "show":
			return runSnapshotShow(args[1:], stdout, stderr, cwd)
		}
	}
	return runSnapshotSave(args, stdin, stdout, stderr, cwd, useStdinAsPipe)
}

func runSnapshotSave(args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string, useStdinAsPipe bool) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fs.SetOutput(stderr)

	name := fs.String("name", "", "snapshot name")
	conn := fs.String("conn", "", "optional connection name for metadata")
	force := fs.Bool("force", false, "overwrite existing snapshot")
	fromLast := fs.Bool("from-last", false, "always save the cached last query result")
	dirFlag := fs.String("dir", "", "snapshots directory (default: .dbx/snapshots under cwd)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	snapName := strings.TrimSpace(*name)
	if snapName == "" {
		return fmt.Errorf("--name is required")
	}
	if err := snapshot.ValidateName(snapName); err != nil {
		return err
	}

	dir := strings.TrimSpace(*dirFlag)
	if dir == "" {
		dir = snapshot.Dir(cwd)
		if err := snapshot.EnsurePrivateDir(filepath.Dir(dir)); err != nil {
			return fmt.Errorf("create default dbx dir: %w", err)
		}
		if err := snapshot.EnsurePrivateDir(dir); err != nil {
			return fmt.Errorf("create default snapshots dir: %w", err)
		}
	}

	data, connection, sqlText, err := resolveSnapshotInput(stdin, cwd, useStdinAsPipe, *fromLast)
	if err != nil {
		return err
	}
	// Explicit --conn overrides last-result connection.
	if c := strings.TrimSpace(*conn); c != "" {
		connection = c
	}

	s := snapshot.NewSnapshot(snapName, data, connection, sqlText, time.Time{})
	path, err := snapshot.Save(dir, s, *force)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintln(stdout, path); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

// resolveSnapshotInput returns normalized data and optional metadata. With
// fromLast it always selects the cached source and rejects actual piped input;
// character-device stdin is not read so interactive use cannot block.
func resolveSnapshotInput(stdin io.Reader, cwd string, useStdinAsPipe, fromLast bool) (data json.RawMessage, connection, sqlText string, err error) {
	if fromLast {
		hasData, err := stdinHasNonWhitespaceData(stdin, useStdinAsPipe)
		if err != nil {
			return nil, "", "", err
		}
		if hasData {
			return nil, "", "", fmt.Errorf("--from-last cannot be combined with nonempty stdin")
		}
		return dataFromLast(cwd)
	}

	// Prefer real pipe detection when stdin is *os.File.
	if f, ok := stdin.(*os.File); ok {
		if isPipedFile(f) {
			return readDataFromReader(f)
		}
		// TTY or non-pipe file → last result
		return dataFromLast(cwd)
	}

	// Non-file readers (tests): honor useStdinAsPipe.
	if useStdinAsPipe {
		return readDataFromReader(stdin)
	}
	return dataFromLast(cwd)
}

// stdinHasNonWhitespaceData checks safely readable stdin for ambiguity. It
// only consumes pipes/redirects (or test readers explicitly marked as pipes),
// never an interactive character device.
func stdinHasNonWhitespaceData(stdin io.Reader, useStdinAsPipe bool) (bool, error) {
	if f, ok := stdin.(*os.File); ok {
		if !isPipedFile(f) {
			return false, nil
		}
		raw, err := io.ReadAll(f)
		if err != nil {
			return false, fmt.Errorf("read stdin: %w", err)
		}
		return len(trimBytes(raw)) > 0, nil
	}
	if !useStdinAsPipe {
		return false, nil
	}
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return false, fmt.Errorf("read stdin: %w", err)
	}
	return len(trimBytes(raw)) > 0, nil
}

func readDataFromReader(r io.Reader) (json.RawMessage, string, string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, "", "", fmt.Errorf("read stdin: %w", err)
	}
	if len(trimBytes(raw)) == 0 {
		return nil, "", "", fmt.Errorf("stdin is empty")
	}
	data, err := snapshot.NormalizeData(raw)
	if err != nil {
		return nil, "", "", err
	}
	return data, "", "", nil
}

func dataFromLast(cwd string) (json.RawMessage, string, string, error) {
	last, err := snapshot.ReadLast(cwd)
	if err != nil {
		return nil, "", "", err
	}
	data, err := snapshot.NormalizeData(last.Data)
	if err != nil {
		return nil, "", "", fmt.Errorf("last result data: %w", err)
	}
	return data, last.Connection, last.SQL, nil
}

func runSnapshotList(args []string, stdout, stderr io.Writer, cwd string) error {
	fs := flag.NewFlagSet("snapshot list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirFlag := fs.String("dir", "", "snapshots directory (default: .dbx/snapshots under cwd)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dir := strings.TrimSpace(*dirFlag)
	if dir == "" {
		dir = snapshot.Dir(cwd)
	}

	entries, err := snapshot.List(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.CreatedAt.IsZero() {
			if _, err := fmt.Fprintln(stdout, e.Name); err != nil {
				return fmt.Errorf("write stdout: %w", err)
			}
			continue
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%s\n", e.Name, e.CreatedAt.UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
	}
	return nil
}

func runSnapshotShow(args []string, stdout, stderr io.Writer, cwd string) error {
	fs := flag.NewFlagSet("snapshot show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirFlag := fs.String("dir", "", "snapshots directory (default: .dbx/snapshots under cwd)")
	dataOnly := fs.Bool("data", false, "print only the data field")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Name is first positional after flags... flag.Parse consumes flags first,
	// remaining args are positional.
	rest := fs.Args()
	if len(rest) < 1 || strings.TrimSpace(rest[0]) == "" {
		return fmt.Errorf("snapshot name is required (usage: dbx snapshot show <name>)")
	}
	name := strings.TrimSpace(rest[0])
	if err := snapshot.ValidateName(name); err != nil {
		return err
	}

	dir := strings.TrimSpace(*dirFlag)
	if dir == "" {
		dir = snapshot.Dir(cwd)
	}

	s, err := snapshot.Load(dir, name)
	if err != nil {
		return err
	}

	var out []byte
	if *dataOnly {
		// Pretty-print data only.
		var pretty bytes.Buffer
		err = json.Indent(&pretty, s.Data, "", "  ")
		if err != nil {
			return fmt.Errorf("format snapshot data: %w", err)
		}
		out = append(pretty.Bytes(), '\n')
	} else {
		out, err = snapshot.EncodeSnapshot(s)
		if err != nil {
			return fmt.Errorf("encode snapshot: %w", err)
		}
	}

	if _, err := stdout.Write(out); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

// isPipedFile reports whether f is not a character device (pipe/redirect/file).
func isPipedFile(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return (st.Mode() & os.ModeCharDevice) == 0
}

func trimBytes(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\n' || b[j-1] == '\r') {
		j--
	}
	return b[i:j]
}
