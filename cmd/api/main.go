package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"go-kpi-tenders/internal/config"
	"go-kpi-tenders/internal/server"
	"go-kpi-tenders/internal/watchdog"
	"go-kpi-tenders/pkg/logging"
)

func main() {
	// ── Config ──────────────────────────────────────
	cfg := config.MustLoad()

	// ── Logger ──────────────────────────────────────
	log := logging.Setup(cfg.AppEnv)
	slog.SetDefault(log)

	log.Info("starting kpi-tenders",
		slog.String("env", cfg.AppEnv),
		slog.String("port", cfg.AppPort),
	)

	// ── Database ────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DBURL)
	if err != nil {
		log.Error("failed to connect to database", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Error("failed to ping database", slog.String("error", err.Error()))
		os.Exit(1)
	}
	log.Info("database connected")

	// ── Server ──────────────────────────────────────
	srv, err := server.NewServer(cfg, log, pool)
	if err != nil {
		log.Error("failed to init server", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:         ":" + cfg.AppPort,
		Handler:      srv.Router(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	log.Info("server started", slog.String("addr", httpSrv.Addr))

	// ── Watchdog ────────────────────────────────────
	watchdogCtx, watchdogCancel := context.WithCancel(context.Background())
	var watchdogDone sync.WaitGroup
	watchdogDone.Add(1)
	go func() {
		defer watchdogDone.Done()
		watchdog.Start(watchdogCtx, srv.DB(), srv.PythonPublisher(), cfg, log)
	}()

	// ── Graceful Shutdown ───────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	log.Info("shutting down", slog.String("signal", sig.String()))

	// Cancel watchdog first so it stops ticking before Redis is closed.
	// If watchdogCancel were only called via defer, the watchdog could tick
	// into a closed Redis pool during the shutdown window.
	watchdogCancel()
	watchdogDone.Wait() // ensure goroutine has exited before Redis is closed

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown", slog.String("error", err.Error()))
		_ = srv.Close()
		os.Exit(1)
	}

	if err := srv.Close(); err != nil {
		log.Error("redis: failed to close publisher", "err", err)
	}

	log.Info("server stopped gracefully")
}
