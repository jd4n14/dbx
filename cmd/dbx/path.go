package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	jsonpath "github.com/jd4n14/dbx/internal/path"
	"github.com/jd4n14/dbx/internal/snapshot"
)

// runPath implements `dbx path [--snapshot <name>] [--dir <path>] <path>`.
func runPath(args []string) error {
	return runPathCmd(args, os.Stdout, os.Stderr, "")
}

// runPathCmd is the testable command entry. It buffers output until both the
// source load and selector evaluation succeed, keeping stdout valid JSON only.
func runPathCmd(args []string, stdout, stderr io.Writer, cwd string) error {
	fs := flag.NewFlagSet("path", flag.ContinueOnError)
	fs.SetOutput(stderr)
	snapshotName := fs.String("snapshot", "", "named snapshot source")
	dirFlag := fs.String("dir", "", "snapshots directory (default: .dbx/snapshots under cwd)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return fmt.Errorf("path requires exactly one selector (usage: dbx path [--snapshot <name>] [--dir <path>] <path>)")
	}

	selector := fs.Args()[0]
	if _, err := jsonpath.Parse(selector); err != nil {
		return err
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}
	}

	name := strings.TrimSpace(*snapshotName)
	var data []byte
	if name == "" && !flagWasProvided(args, "--snapshot") {
		last, err := snapshot.ReadLast(cwd)
		if err != nil {
			return err
		}
		data = last.Data
	} else {
		if err := snapshot.ValidateName(name); err != nil {
			return err
		}
		dir := strings.TrimSpace(*dirFlag)
		if dir == "" {
			dir = snapshot.Dir(cwd)
		} else {
			dir = filepath.Clean(dir)
		}
		s, err := snapshot.Load(dir, name)
		if err != nil {
			return err
		}
		data = s.Data
	}

	matches, err := jsonpath.Evaluate(data, selector)
	if err != nil {
		return err
	}
	out, err := jsonpath.MarshalPretty(matches)
	if err != nil {
		return err
	}
	if _, err := stdout.Write(out); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

// flagWasProvided distinguishes an omitted string flag from --snapshot "" so
// an explicitly empty snapshot source is validated instead of silently using
// last.json.
func flagWasProvided(args []string, name string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}
