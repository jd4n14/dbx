package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/danger"
)

func runDanger(args []string) error {
	return runDangerCmd(args, os.Stdin, os.Stdout, os.Stderr)
}

// runDangerCmd analyzes stdin completely offline. Configuration is loaded only
// to validate an optional named connection and obtain its environment label.
func runDangerCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("danger", flag.ContinueOnError)
	fs.SetOutput(stderr)
	connName := fs.String("conn", "", "named connection for environment context (optional)")
	configPath := fs.String("config", "", "path to config file (optional; requires --conn)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("danger accepts SQL from stdin only")
	}

	name := strings.TrimSpace(*connName)
	if name == "" && strings.TrimSpace(*configPath) != "" {
		return fmt.Errorf("--config requires --conn")
	}
	env := ""
	if name != "" {
		path, err := config.FindConfigPath(*configPath, os.Getenv, "", "")
		if err != nil {
			return err
		}
		cfg, err := config.Load(path)
		if err != nil {
			return err
		}
		conn, err := cfg.Connection(name)
		if err != nil {
			return err
		}
		env = conn.Env
	}

	sql, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	result, err := danger.Analyze(string(sql), env)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode danger result: %w", err)
	}
	out = append(out, '\n')
	if _, err := stdout.Write(out); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}
