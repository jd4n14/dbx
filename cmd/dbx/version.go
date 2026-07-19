package main

// Version is the single source of truth for the dbx build identifier.
// It is exposed through `dbx version`, embedded in export sidecars, and
// stamped into log lines. Plan 008 consumes it as audit metadata only;
// the export command never derives behavior from it.
const Version = "dbx 0.0.1"
