package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	jsondiff "github.com/jd4n14/dbx/internal/diff"
	"github.com/jd4n14/dbx/internal/snapshot"
)

// runDiff implements `dbx diff [--dir <path>] [--json] <before> <after>`.
func runDiff(args []string) error {
	return runDiffCmd(args, os.Stdout, os.Stderr, "")
}

// runDiffCmd is the testable command entry. cwd defaults to os.Getwd when
// empty. It buffers rendered output so errors never leave partial stdout.
func runDiffCmd(args []string, stdout, stderr io.Writer, cwd string) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirFlag := fs.String("dir", "", "snapshots directory (default: .dbx/snapshots under cwd)")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if len(fs.Args()) != 2 {
		return fmt.Errorf("diff requires exactly two snapshot names (usage: dbx diff [--dir <path>] [--json] <before> <after>)")
	}
	beforeName := strings.TrimSpace(fs.Args()[0])
	afterName := strings.TrimSpace(fs.Args()[1])
	if err := snapshot.ValidateName(beforeName); err != nil {
		return err
	}
	if err := snapshot.ValidateName(afterName); err != nil {
		return err
	}

	dir := strings.TrimSpace(*dirFlag)
	if dir == "" {
		if cwd == "" {
			var err error
			cwd, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}
		}
		dir = snapshot.Dir(cwd)
	} else {
		dir = filepath.Clean(dir)
	}

	before, err := snapshot.Load(dir, beforeName)
	if err != nil {
		return err
	}
	after, err := snapshot.Load(dir, afterName)
	if err != nil {
		return err
	}

	changes, err := jsondiff.Compare(before.Data, after.Data)
	if err != nil {
		return fmt.Errorf("compare snapshots: %w", err)
	}

	var out []byte
	if *jsonOutput {
		out, err = jsondiff.RenderJSON(before.Data, after.Data, changes)
	} else {
		out, err = jsondiff.RenderText(changes)
	}
	if err != nil {
		return err
	}
	if _, err := stdout.Write(out); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}
