package config

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	MongoURI      string
	MongoDatabase string
	Port          string
	Env           string
	JWTSecret     string
	AdminPassword string
	AdminUsername string
}

// Load uses flags > process environment > .env > non-secret defaults.
// It never changes the process environment (important for tests and embedding).
func Load(args []string, getenv func(string) string) (Config, error) {
	path := ".env"
	for i, arg := range args {
		if strings.HasPrefix(arg, "-env-file=") || strings.HasPrefix(arg, "--env-file=") {
			path = strings.SplitN(arg, "=", 2)[1]
		}
		if (arg == "-env-file" || arg == "--env-file") && i+1 < len(args) {
			path = args[i+1]
		}
	}
	values := map[string]string{}
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		values, err = parseEnv(f)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	value := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		if v, ok := values[key]; ok {
			return v
		}
		return fallback
	}
	c := Config{}
	fs := flag.NewFlagSet("harbor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&path, "env-file", path, "dotenv file")
	fs.StringVar(&c.MongoURI, "mongo-uri", value("MONGO_URI", ""), "MongoDB replica-set connection string (required)")
	fs.StringVar(&c.MongoDatabase, "mongo-database", value("MONGO_DATABASE", "harbor"), "database")
	fs.StringVar(&c.Port, "port", value("PORT", ""), "HTTP port (required)")
	appEnv := value("APP_ENV", "production")
	if appEnv == "" {
		appEnv = "production"
	}
	fs.StringVar(&c.Env, "app-env", appEnv, "development, test, or production (default production)")
	c.JWTSecret = value("JWT_SECRET", "")
	c.AdminPassword = value("ADMIN_PASSWORD", "")
	c.AdminUsername = value("ADMIN_USERNAME", "admin")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if len(fs.Args()) != 0 {
		return c, fmt.Errorf("unexpected positional arguments")
	}
	if !strings.HasPrefix(c.MongoURI, "mongodb://") && !strings.HasPrefix(c.MongoURI, "mongodb+srv://") {
		return c, fmt.Errorf("MONGO_URI / -mongo-uri must be a MongoDB connection string")
	}
	p, err := strconv.Atoi(c.Port)
	if err != nil || p < 1 || p > 65535 {
		return c, fmt.Errorf("PORT / -port must be between 1 and 65535")
	}
	if c.Env != "development" && c.Env != "production" && c.Env != "test" {
		return c, fmt.Errorf("APP_ENV must be development, test, or production")
	}
	if c.MongoDatabase == "" || strings.ContainsAny(c.MongoDatabase, "/\\. \"$\x00") {
		return c, fmt.Errorf("MONGO_DATABASE is invalid")
	}
	if len(c.JWTSecret) < 32 {
		return c, fmt.Errorf("JWT_SECRET must contain at least 32 bytes (required in every environment)")
	}
	if len(c.AdminPassword) < 12 || len(c.AdminPassword) > 72 {
		return c, fmt.Errorf("ADMIN_PASSWORD must contain 12 to 72 bytes (required in every environment)")
	}
	if strings.TrimSpace(c.AdminUsername) == "" {
		return c, fmt.Errorf("ADMIN_USERNAME is required")
	}
	return c, nil
}

func parseEnv(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid .env assignment")
		}
		if len(val) > 0 && (val[0] == '\'' || val[0] == '"') {
			if len(val) < 2 || val[len(val)-1] != val[0] {
				return nil, fmt.Errorf("unclosed .env quote")
			}
			val = val[1 : len(val)-1]
		} else if at := strings.Index(val, " #"); at >= 0 {
			val = strings.TrimSpace(val[:at])
		}
		out[key] = val
	}
	return out, s.Err()
}
