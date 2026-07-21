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
	case "indexes":
		return runIndexes(args[1:])
	case "fk":
		return runFK(args[1:])
	case "table-size":
		return runTableSize(args[1:])
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
	case "export":
		return runExport(args[1:])
	case "explain":
		return runExplain(args[1:])
	case "version", "--version", "-v":
		fmt.Println(Version)
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
  query        Execute SQL from stdin, print JSON rows to stdout
  ddl          Fetch CREATE TABLE DDL for a table
  tables       List tables (SHOW TABLES) for the connection [optional --schema, --like]
  columns      List columns (SHOW COLUMNS) for a table [optional --like]
  indexes      List indexes (information_schema.STATISTICS) for a table
  fk           List foreign keys (information_schema.KEY_COLUMN_USAGE) for a table
  table-size   Size / engine / row-estimate for a table (information_schema.TABLES)
  snapshot     Save current/last result as a named snapshot
  diff         Structured JSON diff between two snapshots
  path         Apply a path/JSONPath over a result or snapshot
  danger       Analyze SQL for dangerous statements
  history      List / show / clear recent successful dbx query runs
  export       Dump a snapshot to CSV or JSONL (with optional JSON sidecar)
  explain      Pretty-print EXPLAIN <SQL> as a table or JSON plan (with optional sidecar)
  version      Print version
  help         Show this help

Examples:
  dbx query --conn local_wms < query.sql
  dbx ddl --conn local_wms --table orders
  dbx tables --conn local_wms --like ord
  dbx tables --conn local_wms --schema audit --json
  dbx columns --conn local_wms --table orders
  dbx columns --conn local_wms --table orders --json
  dbx indexes --conn local_wms --table orders
  dbx fk --conn local_wms --table order_items
  dbx table-size --conn local_wms --table orders
  dbx snapshot --name before_split_order
  dbx snapshot list
  dbx snapshot show before_split_order
  dbx diff before_split_order after_split_order
  dbx path --snapshot before_split_order 'metadata.fulfillment.status'
  dbx danger < query.sql
  dbx history list --limit 20 --json
  dbx history show 1
  dbx history clear
  dbx export --format csv -o /tmp/before.csv before_split_order
  dbx export --format jsonl --no-json after_split_order
  dbx explain --conn local_wms "SELECT * FROM orders WHERE status = 'pending'"
  dbx explain --json --conn local_wms -o /tmp/plan.json "SELECT 1"
`)
}
