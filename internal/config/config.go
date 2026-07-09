// Package config loads named database connections from YAML and resolves
// passwords according to the password_env truth table.
//
// Discovery order (first match wins; explicit overrides do not fall through):
//  1. --config path (explicit argument)
//  2. DBX_CONFIG environment variable
//  3. ./.dbx/config.yaml (project)
//  4. $XDG_CONFIG_HOME/dbx/config.yaml or ~/.config/dbx/config.yaml (user)
//
// password_env truth table (authoritative):
//
//	password_env non-empty + env set and non-empty → use env; ignore inline password
//	password_env non-empty + env unset or empty    → error (no inline fallback)
//	password_env empty/omitted                     → use inline password (may be empty)
//	raw dsn set                                    → password fields ignored for auth
package config

import "fmt"

// Config is the top-level YAML document.
type Config struct {
	Connections map[string]Connection `yaml:"connections"`
}

// Connection is a named database connection entry.
//
// When returned from Config.Connection / ConnectionWithEnv, Password holds the
// resolved secret (env or inline). When DSN is non-empty, Password is not used
// for authentication (credentials are expected to be embedded in DSN if needed).
type Connection struct {
	// Name is the map key; filled by Connection lookup, not from YAML.
	Name string `yaml:"-"`

	Driver      string `yaml:"driver"`
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	User        string `yaml:"user"`
	Password    string `yaml:"password"`
	PasswordEnv string `yaml:"password_env"`
	Database    string `yaml:"database"`
	// Env is an optional label: dev | staging | prod | readonly.
	Env string `yaml:"env"`
	// DSN, when non-empty, is the base MySQL DSN. Host/user/password/database
	// fields are not required and are not used for auth (Phase 3a force-safe DSN).
	DSN string `yaml:"dsn"`
}

// AllowedEnvLabels are valid values for Connection.Env when the field is set.
var AllowedEnvLabels = map[string]struct{}{
	"dev":      {},
	"staging":  {},
	"prod":     {},
	"readonly": {},
}

// DefaultMySQLPort is used when port is omitted for field-built connections.
const DefaultMySQLPort = 3306

func validateEnvLabel(label string) error {
	if label == "" {
		return nil
	}
	if _, ok := AllowedEnvLabels[label]; !ok {
		return fmt.Errorf("invalid env %q: must be one of dev, staging, prod, readonly", label)
	}
	return nil
}
