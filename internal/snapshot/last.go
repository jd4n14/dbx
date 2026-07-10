package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LastPath returns the path of the last query result cache under cwd.
func LastPath(cwd string) string {
	return filepath.Join(cwd, ".dbx", "last.json")
}

// WriteLast persists a last_result envelope atomically under cwd/.dbx/last.json.
func WriteLast(cwd string, r LastResult) error {
	if r.Type == "" {
		r.Type = TypeLastResult
	}
	body, err := EncodeLastResult(r)
	if err != nil {
		return fmt.Errorf("encode last result: %w", err)
	}
	path := LastPath(cwd)
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("create last result dir: %w", err)
	}
	if err := atomicWrite(path, body, 0o600); err != nil {
		return fmt.Errorf("write last result: %w", err)
	}
	return nil
}

// WriteLastFromQueryData normalizes query JSON output and writes last_result.
// dataJSON should be the pretty (or compact) array/object bytes that query prints.
func WriteLastFromQueryData(cwd, connection, sql string, dataJSON []byte) error {
	data, err := NormalizeData(dataJSON)
	if err != nil {
		return fmt.Errorf("last result data: %w", err)
	}
	return WriteLast(cwd, NewLastResult(data, connection, sql, time.Time{}))
}

// ReadLast loads the last_result envelope from cwd/.dbx/last.json.
func ReadLast(cwd string) (LastResult, error) {
	path := LastPath(cwd)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LastResult{}, fmt.Errorf("no last result; run dbx query first or pipe JSON on stdin")
		}
		return LastResult{}, fmt.Errorf("read last result: %w", err)
	}
	var r LastResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return LastResult{}, fmt.Errorf("parse last result: %w", err)
	}
	if r.Type != TypeLastResult {
		return LastResult{}, fmt.Errorf("invalid last result envelope type")
	}
	if len(r.Data) == 0 {
		return LastResult{}, fmt.Errorf("last result has empty data")
	}
	if !json.Valid(r.Data) {
		return LastResult{}, fmt.Errorf("last result has invalid data")
	}
	return r, nil
}
