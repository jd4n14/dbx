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
	case "tables":
		return runTables(args[1:])
	case "columns":
		return runColumns(args[1:])
	case "snapshot":
		return runSnapshot(args[1:])
	case "diff":
		return runDiff(args[1:])
	case "path":
		return runPath(args[1:])
	case "danger":
		return runDanger(args[1:])
	case "history":
		return runHistory(args[1:])
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
  tables     List tables (SHOW TABLES) for the connection [optional --schema, --like]
  columns    List columns (SHOW COLUMNS) for a table [optional --like]
  snapshot   Save current/last result as a named snapshot
  diff       Structured JSON diff between two snapshots
  path       Apply a path/JSONPath over a result or snapshot
  danger     Analyze SQL for dangerous statements
  history    List / show / clear recent successful dbx query runs
  version    Print version
  help       Show this help

Examples:
  dbx query --conn local_wms < query.sql
  dbx ddl --conn local_wms --table orders
  dbx tables --conn local_wms --like ord
  dbx tables --conn local_wms --schema audit --json
  dbx columns --conn local_wms --table orders
  dbx columns --conn local_wms --table orders --json
  dbx snapshot --name before_split_order
  dbx snapshot list
  dbx snapshot show before_split_order
  dbx diff before_split_order after_split_order
  dbx path --snapshot before_split_order 'metadata.fulfillment.status'
  dbx danger < query.sql
  dbx history list --limit 20 --json
  dbx history show 1
  dbx history clear
`)
}
