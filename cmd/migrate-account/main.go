// migrate-account assigns legacy single-business data to its original owner.
// Run offline, after a backup; never run while the old application is writing.
package main

import (
	"context"
	"flag"
	"harbor/internal/config"
	"harbor/internal/database"
	"harbor/internal/store"
	"log/slog"
	"os"
	"time"
)

func main() {
	business := flag.String("business-name", "", "name of the existing business (required)")
	owner := flag.String("owner-username", "", "existing admin username (defaults to ADMIN_USERNAME)")
	envFile := flag.String("env-file", ".env", "application configuration file")
	flag.Parse()
	if *business == "" {
		slog.Error("-business-name is required")
		os.Exit(1)
	}
	c, err := config.Load([]string{"-env-file", *envFile}, os.Getenv)
	if err != nil {
		slog.Error("Invalid configuration", "error", err)
		os.Exit(1)
	}
	if *owner == "" {
		*owner = c.AdminUsername
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := database.Open(ctx, c)
	if err != nil {
		slog.Error("Database connection failed", "error", err)
		os.Exit(1)
	}
	defer db.Client.Disconnect(context.Background())
	account, err := store.New(db, c).MigrateLegacyAccount(ctx, *owner, *business)
	if err != nil {
		slog.Error("Migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Legacy business migrated", "accountId", account.AccountID, "businessName", account.BusinessName)
}
