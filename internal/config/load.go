package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads and unmarshals a YAML config file from path.
// It does not resolve passwords or fully validate individual connections;
// call Connection for validation and password resolution.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found: %s", path)
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if len(cfg.Connections) == 0 {
		return nil, fmt.Errorf("config %s: no connections defined", path)
	}
	return &cfg, nil
}

// FindConfigPath returns the first config path per discovery order.
//
//   - explicit: --config flag value; if non-empty, that path must exist (no fallthrough)
//   - getenv: environment lookup (defaults to os.Getenv); used for DBX_CONFIG and XDG_CONFIG_HOME
//   - cwd: project root for ./.dbx/config.yaml (defaults to os.Getwd)
//   - home: user home for ~/.config/dbx/config.yaml (defaults to os.UserHomeDir)
//
// When an override (explicit or DBX_CONFIG) is set but the file is missing,
// FindConfigPath returns an error and does not fall through to lower-priority paths.
func FindConfigPath(explicit string, getenv func(string) string, cwd, home string) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory: %w", err)
		}
	}
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
	}

	var searched []string

	if explicit != "" {
		searched = append(searched, explicit)
		if fileExists(explicit) {
			return explicit, nil
		}
		return "", fmt.Errorf("config file not found: %s", explicit)
	}

	if v := strings.TrimSpace(getenv("DBX_CONFIG")); v != "" {
		searched = append(searched, v)
		if fileExists(v) {
			return v, nil
		}
		return "", fmt.Errorf("config file not found (DBX_CONFIG): %s", v)
	}

	projectPath := filepath.Join(cwd, ".dbx", "config.yaml")
	searched = append(searched, projectPath)
	if fileExists(projectPath) {
		return projectPath, nil
	}

	userPath := userConfigPath(getenv, home)
	searched = append(searched, userPath)
	if fileExists(userPath) {
		return userPath, nil
	}

	return "", fmt.Errorf("config file not found; searched: %s", strings.Join(searched, ", "))
}

func userConfigPath(getenv func(string) string, home string) string {
	if xdg := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "dbx", "config.yaml")
	}
	return filepath.Join(home, ".config", "dbx", "config.yaml")
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// EnvLookup reports whether an environment variable is present and its value.
// Matches os.LookupEnv so tests can distinguish unset from empty.
type EnvLookup func(key string) (value string, ok bool)

// Connection returns the named connection with driver/field validation and
// password resolution using os.LookupEnv (password_env truth table).
func (c *Config) Connection(name string) (Connection, error) {
	return c.ConnectionWithLookup(name, os.LookupEnv)
}

// ConnectionWithLookup is like Connection but uses lookup for password_env.
// lookup must not be nil.
func (c *Config) ConnectionWithLookup(name string, lookup EnvLookup) (Connection, error) {
	if c == nil || c.Connections == nil {
		return Connection{}, fmt.Errorf("connection %q not found: no connections loaded", name)
	}
	raw, ok := c.Connections[name]
	if !ok {
		return Connection{}, fmt.Errorf("connection %q not found", name)
	}
	if lookup == nil {
		return Connection{}, fmt.Errorf("connection %q: env lookup is nil", name)
	}

	conn := raw
	conn.Name = name

	if err := validateAndNormalize(&conn, lookup); err != nil {
		return Connection{}, err
	}
	return conn, nil
}

func validateAndNormalize(conn *Connection, lookup EnvLookup) error {
	// Driver: empty defaults to mysql; reject anything else.
	driver := strings.TrimSpace(conn.Driver)
	if driver == "" {
		driver = "mysql"
	}
	if driver != "mysql" {
		return fmt.Errorf("connection %q: unsupported driver %q (only mysql is supported)", conn.Name, driver)
	}
	conn.Driver = driver

	if err := validateEnvLabel(conn.Env); err != nil {
		return fmt.Errorf("connection %q: %w", conn.Name, err)
	}

	// Raw DSN: host/user/password/database not required; password fields ignored for auth.
	if strings.TrimSpace(conn.DSN) != "" {
		conn.DSN = strings.TrimSpace(conn.DSN)
		return nil
	}

	if strings.TrimSpace(conn.Host) == "" {
		return fmt.Errorf("connection %q: host is required when dsn is not set", conn.Name)
	}
	if strings.TrimSpace(conn.User) == "" {
		return fmt.Errorf("connection %q: user is required when dsn is not set", conn.Name)
	}
	if strings.TrimSpace(conn.Database) == "" {
		return fmt.Errorf("connection %q: database is required when dsn is not set", conn.Name)
	}
	if conn.Port == 0 {
		conn.Port = DefaultMySQLPort
	}

	resolved, err := resolvePassword(conn.Name, conn.Password, conn.PasswordEnv, lookup)
	if err != nil {
		return err
	}
	conn.Password = resolved
	return nil
}

// resolvePassword implements the password_env truth table.
// Never includes password values in error messages.
func resolvePassword(connName, inlinePassword, passwordEnv string, lookup EnvLookup) (string, error) {
	envKey := strings.TrimSpace(passwordEnv)
	if envKey == "" {
		// password_env omitted/empty → use inline password (may be empty).
		return inlinePassword, nil
	}

	// password_env non-empty: env must be set and non-empty; never fall back to inline.
	val, ok := lookup(envKey)
	if !ok {
		return "", fmt.Errorf("connection %q: password_env %q is not set", connName, envKey)
	}
	if val == "" {
		return "", fmt.Errorf("connection %q: password_env %q is set but empty", connName, envKey)
	}
	// Use env value; ignore inline password.
	return val, nil
}
