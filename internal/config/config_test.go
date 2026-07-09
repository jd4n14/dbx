package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func envMap(m map[string]string) EnvLookup {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

const twoConnYAML = `
connections:
  local_wms:
    driver: mysql
    host: 127.0.0.1
    port: 3306
    user: root
    password: secret
    database: wms
    env: dev
  staging_ro:
    host: db.example.com
    user: ro
    password: ro-pass
    database: wms
    env: staging
`

func TestLoadValidTwoConnections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, twoConnYAML)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	c1, err := cfg.ConnectionWithLookup("local_wms", envMap(nil))
	if err != nil {
		t.Fatalf("Connection local_wms: %v", err)
	}
	if c1.Name != "local_wms" {
		t.Errorf("Name = %q, want local_wms", c1.Name)
	}
	if c1.Driver != "mysql" {
		t.Errorf("Driver = %q, want mysql", c1.Driver)
	}
	if c1.Host != "127.0.0.1" || c1.User != "root" || c1.Database != "wms" {
		t.Errorf("fields = %+v", c1)
	}
	if c1.Password != "secret" {
		t.Errorf("Password = %q, want secret", c1.Password)
	}
	if c1.Port != 3306 {
		t.Errorf("Port = %d, want 3306", c1.Port)
	}
	if c1.Env != "dev" {
		t.Errorf("Env = %q, want dev", c1.Env)
	}

	c2, err := cfg.ConnectionWithLookup("staging_ro", envMap(nil))
	if err != nil {
		t.Fatalf("Connection staging_ro: %v", err)
	}
	// driver omitted defaults to mysql
	if c2.Driver != "mysql" {
		t.Errorf("default Driver = %q, want mysql", c2.Driver)
	}
	if c2.Password != "ro-pass" {
		t.Errorf("Password = %q, want ro-pass", c2.Password)
	}
	// port omitted defaults to 3306
	if c2.Port != DefaultMySQLPort {
		t.Errorf("default Port = %d, want %d", c2.Port, DefaultMySQLPort)
	}
}

func TestConnectionUnknownName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, twoConnYAML)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = cfg.ConnectionWithLookup("no_such", envMap(nil))
	if err == nil {
		t.Fatal("expected error for unknown connection")
	}
	if !strings.Contains(err.Error(), "no_such") {
		t.Errorf("error should mention name: %v", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "ro-pass") {
		t.Errorf("error must not leak passwords: %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found: %v", err)
	}
}

func TestLoadBadYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, "connections: [\n  not: valid\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Errorf("error should mention parse: %v", err)
	}
}

func TestLoadEmptyConnections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, "connections: {}\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty connections")
	}
}

func TestPasswordEnvMatrix(t *testing.T) {
	// Base YAML uses password_env; individual cases override via inline structs.
	tests := []struct {
		name        string
		yaml        string
		conn        string
		env         map[string]string
		wantPass    string
		wantErrSub  string
	}{
		{
			name: "password_env set + env non-empty uses env ignores inline",
			yaml: `
connections:
  c:
    host: h
    user: u
    password: inline-secret
    password_env: MYSQL_PASSWORD
    database: d
`,
			conn:     "c",
			env:      map[string]string{"MYSQL_PASSWORD": "from-env"},
			wantPass: "from-env",
		},
		{
			name: "password_env set + env unset errors no inline fallback",
			yaml: `
connections:
  c:
    host: h
    user: u
    password: inline-secret
    password_env: MYSQL_PASSWORD
    database: d
`,
			conn:       "c",
			env:        map[string]string{}, // key absent
			wantErrSub: "not set",
		},
		{
			name: "password_env set + env empty string errors no inline fallback",
			yaml: `
connections:
  c:
    host: h
    user: u
    password: inline-secret
    password_env: MYSQL_PASSWORD
    database: d
`,
			conn:       "c",
			env:        map[string]string{"MYSQL_PASSWORD": ""},
			wantErrSub: "empty",
		},
		{
			name: "password_env omitted uses inline password",
			yaml: `
connections:
  c:
    host: h
    user: u
    password: inline-only
    database: d
`,
			conn:     "c",
			env:      nil,
			wantPass: "inline-only",
		},
		{
			name: "password_env omitted empty inline allowed",
			yaml: `
connections:
  c:
    host: h
    user: u
    password: ""
    database: d
`,
			conn:     "c",
			env:      nil,
			wantPass: "",
		},
		{
			name: "raw dsn present password fields not required",
			yaml: `
connections:
  c:
    dsn: "user:pass@tcp(127.0.0.1:3306)/wms"
`,
			conn:     "c",
			env:      nil,
			wantPass: "", // not resolved; ignored for auth
		},
		{
			name: "raw dsn present ignores password_env even if set",
			yaml: `
connections:
  c:
    dsn: "user:pass@tcp(127.0.0.1:3306)/wms"
    password: inline
    password_env: MYSQL_PASSWORD
`,
			conn:     "c",
			env:      map[string]string{}, // would error if password_env were resolved
			wantPass: "inline",            // left as-is; not used for auth
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			writeFile(t, path, tt.yaml)

			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			conn, err := cfg.ConnectionWithLookup(tt.conn, envMap(tt.env))
			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErrSub)
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrSub)
				}
				// Must not fall back: error message must not claim success; must not leak inline secret.
				if strings.Contains(err.Error(), "inline-secret") {
					t.Errorf("error leaked inline password: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Connection: %v", err)
			}
			if conn.Password != tt.wantPass {
				t.Errorf("Password = %q, want %q", conn.Password, tt.wantPass)
			}
			// Never leak secrets in Name-only identity.
			if conn.Name != tt.conn {
				t.Errorf("Name = %q, want %q", conn.Name, tt.conn)
			}
		})
	}
}

func TestUnsupportedDriver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, `
connections:
  c:
    driver: postgres
    host: h
    user: u
    database: d
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = cfg.ConnectionWithLookup("c", envMap(nil))
	if err == nil || !strings.Contains(err.Error(), "postgres") {
		t.Fatalf("expected unsupported driver error, got %v", err)
	}
}

func TestInvalidEnvLabel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, `
connections:
  c:
    host: h
    user: u
    database: d
    env: production
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = cfg.ConnectionWithLookup("c", envMap(nil))
	if err == nil || !strings.Contains(err.Error(), "invalid env") {
		t.Fatalf("expected invalid env error, got %v", err)
	}
}

func TestMissingRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		sub  string
	}{
		{
			name: "missing host",
			yaml: `
connections:
  c:
    user: u
    database: d
`,
			sub: "host",
		},
		{
			name: "missing user",
			yaml: `
connections:
  c:
    host: h
    database: d
`,
			sub: "user",
		},
		{
			name: "missing database",
			yaml: `
connections:
  c:
    host: h
    user: u
`,
			sub: "database",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			writeFile(t, path, tt.yaml)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			_, err = cfg.ConnectionWithLookup("c", envMap(nil))
			if err == nil || !strings.Contains(err.Error(), tt.sub) {
				t.Fatalf("expected error about %q, got %v", tt.sub, err)
			}
		})
	}
}

func TestFindConfigPathDiscoveryOrder(t *testing.T) {
	// Layout:
	//   root/
	//     explicit.yaml
	//     via_env.yaml
	//     project_cwd/.dbx/config.yaml
	//     home/.config/dbx/config.yaml
	//     xdg/dbx/config.yaml
	root := t.TempDir()
	explicit := filepath.Join(root, "explicit.yaml")
	viaEnv := filepath.Join(root, "via_env.yaml")
	projectCwd := filepath.Join(root, "project_cwd")
	projectCfg := filepath.Join(projectCwd, ".dbx", "config.yaml")
	home := filepath.Join(root, "home")
	userCfg := filepath.Join(home, ".config", "dbx", "config.yaml")
	xdg := filepath.Join(root, "xdg")
	xdgCfg := filepath.Join(xdg, "dbx", "config.yaml")

	writeFile(t, explicit, "connections:\n  e: {dsn: a}\n")
	writeFile(t, viaEnv, "connections:\n  e: {dsn: a}\n")
	writeFile(t, projectCfg, "connections:\n  e: {dsn: a}\n")
	writeFile(t, userCfg, "connections:\n  e: {dsn: a}\n")
	writeFile(t, xdgCfg, "connections:\n  e: {dsn: a}\n")

	t.Run("explicit beats all", func(t *testing.T) {
		got, err := FindConfigPath(explicit, func(string) string {
			return viaEnv // would win if explicit ignored
		}, projectCwd, home)
		if err != nil {
			t.Fatal(err)
		}
		if got != explicit {
			t.Errorf("got %q, want %q", got, explicit)
		}
	})

	t.Run("DBX_CONFIG beats project and user", func(t *testing.T) {
		got, err := FindConfigPath("", func(k string) string {
			if k == "DBX_CONFIG" {
				return viaEnv
			}
			return ""
		}, projectCwd, home)
		if err != nil {
			t.Fatal(err)
		}
		if got != viaEnv {
			t.Errorf("got %q, want %q", got, viaEnv)
		}
	})

	t.Run("project beats user", func(t *testing.T) {
		got, err := FindConfigPath("", func(string) string { return "" }, projectCwd, home)
		if err != nil {
			t.Fatal(err)
		}
		if got != projectCfg {
			t.Errorf("got %q, want %q", got, projectCfg)
		}
	})

	t.Run("user when no project", func(t *testing.T) {
		emptyCwd := filepath.Join(root, "empty_cwd")
		if err := os.MkdirAll(emptyCwd, 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := FindConfigPath("", func(string) string { return "" }, emptyCwd, home)
		if err != nil {
			t.Fatal(err)
		}
		if got != userCfg {
			t.Errorf("got %q, want %q", got, userCfg)
		}
	})

	t.Run("XDG_CONFIG_HOME used for user path", func(t *testing.T) {
		emptyCwd := filepath.Join(root, "empty_cwd2")
		if err := os.MkdirAll(emptyCwd, 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := FindConfigPath("", func(k string) string {
			if k == "XDG_CONFIG_HOME" {
				return xdg
			}
			return ""
		}, emptyCwd, home)
		if err != nil {
			t.Fatal(err)
		}
		if got != xdgCfg {
			t.Errorf("got %q, want %q", got, xdgCfg)
		}
	})

	t.Run("explicit missing does not fall through", func(t *testing.T) {
		missing := filepath.Join(root, "nope.yaml")
		_, err := FindConfigPath(missing, func(string) string { return "" }, projectCwd, home)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), missing) {
			t.Errorf("error should mention path: %v", err)
		}
	})

	t.Run("DBX_CONFIG missing does not fall through", func(t *testing.T) {
		missing := filepath.Join(root, "nope_env.yaml")
		_, err := FindConfigPath("", func(k string) string {
			if k == "DBX_CONFIG" {
				return missing
			}
			return ""
		}, projectCwd, home)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "DBX_CONFIG") {
			t.Errorf("error should mention DBX_CONFIG: %v", err)
		}
	})

	t.Run("none found lists searched paths", func(t *testing.T) {
		emptyRoot := t.TempDir()
		emptyCwd := filepath.Join(emptyRoot, "cwd")
		emptyHome := filepath.Join(emptyRoot, "home")
		if err := os.MkdirAll(emptyCwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(emptyHome, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := FindConfigPath("", func(string) string { return "" }, emptyCwd, emptyHome)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "searched") {
			t.Errorf("error should list searched paths: %v", err)
		}
		if !strings.Contains(err.Error(), filepath.Join(emptyCwd, ".dbx", "config.yaml")) {
			t.Errorf("error should include project path: %v", err)
		}
	})
}
