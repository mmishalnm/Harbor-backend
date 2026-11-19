package app

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"harbor/internal/api"
	"harbor/internal/config"
	"harbor/internal/database"
	"harbor/internal/store"
)

// Run starts Harbor and shuts it down gracefully on SIGINT or SIGTERM.
func Run(args []string, getenv func(string) string) error {
	c, err := config.Load(args, getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := database.Connect(startup, c)
	if err != nil {
		return err
	}
	defer func() {
		closing, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = db.Client.Disconnect(closing)
	}()
	s := store.New(db, c)
	if err := s.Bootstrap(startup, c.AdminUsername, c.AdminPassword); err != nil {
		return err
	}
	server := &http.Server{Addr: net.JoinHostPort("", c.Port), Handler: api.New(s, slog.Default()), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	slog.Info("Harbor listening", "env", c.Env, "port", c.Port, "docs", "/docs")
	select {
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	closing, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	return server.Shutdown(closing)
}
