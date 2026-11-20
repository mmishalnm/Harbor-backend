package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testEnv() map[string]string {
	return map[string]string{"MONGO_URI": "mongodb://example.invalid:27099/?replicaSet=rs0", "PORT": "8088", "JWT_SECRET": strings.Repeat("s", 32), "ADMIN_PASSWORD": "test-password-long"}
}
func TestPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.env")
	if err := os.WriteFile(path, []byte("PORT=1111\nMONGO_URI='mongodb://from-file:27888'\nJWT_SECRET="+strings.Repeat("x", 32)+"\nADMIN_PASSWORD=from-file-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PORT": "2222"}
	c, err := Load([]string{"-env-file", path, "-port=3333"}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != "3333" || c.MongoURI != "mongodb://from-file:27888" || c.AdminPassword != "from-file-secret" {
		t.Fatalf("precedence failed: port=%s, URI=%s", c.Port, c.MongoURI)
	}
}
func TestApplicationEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name        string
		dotenv      string
		environment string
		flags       []string
		want        string
		invalid     bool
	}{
		{name: "not supplied", want: "production"},
		{name: "empty dotenv value", dotenv: "APP_ENV=\n", want: "production"},
		{name: "dotenv override", dotenv: "APP_ENV=development\n", want: "development"},
		{name: "environment overrides dotenv", dotenv: "APP_ENV=development\n", environment: "test", want: "test"},
		{name: "flag overrides environment and dotenv", dotenv: "APP_ENV=test\n", environment: "production", flags: []string{"-app-env=development"}, want: "development"},
		{name: "invalid environment", environment: "staging", invalid: true},
		{name: "explicit empty flag", flags: []string{"-app-env="}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.env")
			if tc.dotenv != "" {
				if err := os.WriteFile(path, []byte(tc.dotenv), 0600); err != nil {
					t.Fatal(err)
				}
			}
			env := testEnv()
			env["APP_ENV"] = tc.environment
			args := append([]string{"-env-file", path}, tc.flags...)
			c, err := Load(args, func(k string) string { return env[k] })
			if tc.invalid {
				if err == nil {
					t.Fatal("expected invalid environment to fail")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Env != tc.want {
				t.Fatalf("environment=%q want %q", c.Env, tc.want)
			}
		})
	}
}

func TestDefaultProductionRequiresSecrets(t *testing.T) {
	for _, key := range []string{"JWT_SECRET", "ADMIN_PASSWORD"} {
		t.Run(key, func(t *testing.T) {
			env := testEnv()
			delete(env, key)
			_, err := Load([]string{"-env-file", filepath.Join(t.TempDir(), "absent")}, func(k string) string { return env[k] })
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("expected required %s error, got %v", key, err)
			}
		})
	}
}

func TestRequiredConfig(t *testing.T) {
	for _, key := range []string{"MONGO_URI", "PORT", "JWT_SECRET", "ADMIN_PASSWORD"} {
		t.Run(key, func(t *testing.T) {
			env := testEnv()
			env["APP_ENV"] = "production"
			delete(env, key)
			_, err := Load([]string{"-env-file", filepath.Join(t.TempDir(), "absent")}, func(k string) string { return env[k] })
			if err == nil {
				t.Fatal("expected failure")
			}
		})
	}
	for _, value := range []string{"0", "65536", "abc", "-1"} {
		env := testEnv()
		env["PORT"] = value
		if _, err := Load(nil, func(k string) string { return env[k] }); err == nil {
			t.Fatalf("accepted port %s", value)
		}
	}
}
func TestDotEnv(t *testing.T) {
	m, err := parseEnv(strings.NewReader("# comment\nexport PORT = 1234 # note\nJWT_SECRET=\"a # b\"\nPASSWORD='a=b'\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m["PORT"] != "1234" || m["JWT_SECRET"] != "a # b" || m["PASSWORD"] != "a=b" {
		t.Fatal(m)
	}
	if _, err := parseEnv(strings.NewReader("BAD='unclosed")); err == nil {
		t.Fatal("accepted unclosed quote")
	}
}

func TestPlatformBootstrapConfig(t *testing.T) {
	for _, pair := range [][2]string{{"owner", ""}, {"", "Long-password-123"}, {"owner", "short"}, {"admin", "Long-password-123"}, {"bad name", "Long-password-123"}} {
		env := testEnv()
		env["SUPERADMIN_USERNAME"] = pair[0]
		env["SUPERADMIN_PASSWORD"] = pair[1]
		if _, err := Load([]string{"-env-file", filepath.Join(t.TempDir(), "absent")}, func(k string) string { return env[k] }); err == nil {
			t.Fatal("invalid platform bootstrap accepted", pair[0])
		}
	}
	env := testEnv()
	env["SUPERADMIN_USERNAME"] = "harbor.owner"
	env["SUPERADMIN_PASSWORD"] = "Long-password-123"
	c, err := Load([]string{"-env-file", filepath.Join(t.TempDir(), "absent")}, func(k string) string { return env[k] })
	if err != nil || c.SuperAdminUsername != "harbor.owner" {
		t.Fatal(err)
	}
}
