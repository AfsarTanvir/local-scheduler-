// Command local-scheduler runs the job scheduler and its HTTP API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata" // embed timezone data, so job timezones work on Windows and in small images

	"github.com/AfsarTanvir/local-scheduler-/internal/api"
	"github.com/AfsarTanvir/local-scheduler-/internal/runner"
	"github.com/AfsarTanvir/local-scheduler-/internal/scheduler"
	"github.com/AfsarTanvir/local-scheduler-/internal/store"
)

// shutdownTimeout is how long running jobs and requests get to finish on shutdown.
const shutdownTimeout = 30 * time.Second

func main() {
	port := getenv("PORT", "8080")
	dbPath := getenv("DB_PATH", filepath.Join("data", "scheduler.db"))
	allowShell := os.Getenv("ALLOW_SHELL_JOBS") == "true"
	if allowShell {
		slog.Warn("shell jobs are enabled: anyone who can reach the API can run commands on this machine")
	}

	st, err := store.Open(dbPath)
	if err != nil {
		slog.Error("cannot open database", "path", dbPath, "err", err)
		os.Exit(1)
	}
	defer st.Close()
	slog.Info("database opened", "path", dbPath)

	// Runs that were in progress when the process last died can't still be
	// running. Mark them failed, so their jobs are free to run again.
	n, err := st.RecoverInterrupted(time.Now())
	if err != nil {
		slog.Error("crash recovery failed", "err", err)
		os.Exit(1)
	}
	if n > 0 {
		slog.Warn("marked runs interrupted by the last shutdown as failed", "count", n)
	}

	// Wire the parts together: the API and the scheduler share one store.
	sched := scheduler.New(st, runner.Run)
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           api.New(st, sched, allowShell).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// ctx is canceled on Ctrl+C (SIGINT) or `docker stop` (SIGTERM).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sched.Start()
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	// First stop accepting API requests, then let running jobs finish.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown failed", "err", err)
	}
	if err := sched.Stop(shutdownCtx); err != nil {
		slog.Error("scheduler shutdown failed", "err", err)
	}
	slog.Info("stopped")
}

// getenv returns the environment variable key, or fallback when it is not set.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
