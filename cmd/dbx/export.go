package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jd4n14/dbx/internal/export"
	"github.com/jd4n14/dbx/internal/snapshot"
)

// runExport dispatches `dbx export <snapshot-id> [--format csv|jsonl] [-o FILE] [--json|--no-json]`.
func runExport(args []string) error {
	return runExportCmd(args, os.Stdout, os.Stderr, "")
}

// runExportCmd is the testable entry. cwd defaults to os.Getwd when empty.
//
// Behavior matches Plan 008:
//   - --format defaults to csv
//   - --json sidecar default ON; --no-json opt-out
//   - -o FILE: required iff the default path cannot be resolved (it can,
//     so the flag is optional but tested in both directions)
//   - Errors are friendly, single short line; exit non-zero via run().
//   - Atomic writes; sidecar (when enabled) written first.
func runExportCmd(args []string, stdout, stderr io.Writer, cwd string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	formatStr := fs.String("format", "csv", "output format: csv or jsonl (default csv)")
	outPath := fs.String("o", "", "output path (default: <snapshot-id>.<ext> in cwd)")
	dirFlag := fs.String("dir", "", "snapshots directory (default: .dbx/snapshots under cwd)")
	// --json defaults true; flag exposes an optional boolVar so the parser
	// can distinguish "user passed --json" from "user passed nothing".
	writeJSON := fs.Bool("json", true, "write JSON sidecar with audit metadata (default ON)")
	// Sentinel so we know whether the user wrote --no-json (true) or
	// nothing (false). Keep the boolean inversion symmetrical.
	noJSON := fs.Bool("no-json", false, "disable the JSON sidecar (overrides --json)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}
	}

	rest := fs.Args()
	if len(rest) < 1 || strings.TrimSpace(rest[0]) == "" {
		return fmt.Errorf("export requires a snapshot id (usage: dbx export <snapshot-id> [--format csv|jsonl] [-o FILE] [--json|--no-json])")
	}
	snapshotID := strings.TrimSpace(rest[0])
	if err := snapshot.ValidateName(snapshotID); err != nil {
		return err
	}

	format, err := export.ValidateFormat(*formatStr)
	if err != nil {
		return err
	}

	dir := strings.TrimSpace(*dirFlag)
	if dir == "" {
		dir = snapshot.Dir(cwd)
	}

	// --no-json wins over --json so the more specific opt-out always wins.
	emitSidecar := *writeJSON && !*noJSON

	// Resolve the output path before reading the snapshot so missing /
	// unwritable targets fail fast with a clear error.
	resolvedPath := strings.TrimSpace(*outPath)
	if resolvedPath == "" {
		defaultPath, err := export.DefaultPath(snapshotID, format, "")
		if err != nil {
			return err
		}
		// Join with cwd so callers get an absolute path back.
		resolvedPath = filepath.Join(cwd, defaultPath)
	}

	snap, err := snapshot.Load(dir, snapshotID)
	if err != nil {
		return err
	}

	res, err := export.Write(resolvedPath, snap.Data, export.Options{
		SnapshotID: snapshotID,
		Connection: snap.Connection,
		Format:     format,
		WriteJSON:  emitSidecar,
		Version:    Version,
	})
	if err != nil {
		// Distinguish fs errors from data-shape errors: a friendly line is
		// already returned by export.Write; just propagate.
		return err
	}

	if _, err := fmt.Fprintf(stdout, "%s\n", res.DataPath); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	if emitSidecar && res.Sidecar != "" {
		if _, err := fmt.Fprintf(stdout, "%s\n", res.Sidecar); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
	}
	return nil
}
