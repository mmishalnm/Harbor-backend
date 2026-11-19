package main

import (
	"log/slog"
	"os"

	"harbor/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:], os.Getenv); err != nil {
		slog.Error("Harbor stopped", "error", err)
		os.Exit(1)
	}
}
