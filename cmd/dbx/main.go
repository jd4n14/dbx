package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "query":
		return runQuery(args[1:])
	case "ddl":
		return runDDL(args[1:])
	case "snapshot", "diff", "path", "danger":
		fmt.Fprintf(os.Stderr, "command %q is not implemented yet\n", args[0])
		return fmt.Errorf("not implemented")
	case "version", "--version", "-v":
		fmt.Println("dbx 0.0.1")
		return nil
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func printUsage() {
	fmt.Fprintf(os.Stdout, `dbx — SQL/JSON console for Neovim

Usage:
  dbx <command> [flags]

Commands:
  query      Execute SQL from stdin, print JSON rows to stdout
  ddl        Fetch CREATE TABLE DDL for a table
  snapshot   Save current/last result as a named snapshot
  diff       Structured JSON diff between two snapshots
  path       Apply a path/JSONPath over a result or snapshot
  danger     Analyze SQL for dangerous statements
  version    Print version
  help       Show this help

Examples:
  dbx query --conn local_wms < query.sql
  dbx ddl --conn local_wms --table orders
  dbx snapshot --name before_split_order
  dbx diff before_split_order after_split_order
  dbx path --snapshot before_split_order 'metadata.fulfillment.status'
  dbx danger < query.sql
`)
}
