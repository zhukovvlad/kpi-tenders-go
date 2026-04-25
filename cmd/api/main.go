package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"go-kpi-tenders/internal/config"
	"go-kpi-tenders/internal/server"
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
		log.Error("failed to init server", slog.String("error", err.Error()))
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

	// ── Graceful Shutdown ───────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	log.Info("shutting down", slog.String("signal", sig.String()))

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("forced shutdown", slog.String("error", err.Error()))
		os.Exit(1)
	}

	log.Info("server stopped gracefully")
}
