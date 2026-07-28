package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"autobuff-monitor/server/internal/api"
	"autobuff-monitor/server/internal/auth"
	"autobuff-monitor/server/internal/config"
	"autobuff-monitor/server/internal/database"
	"autobuff-monitor/server/internal/expgain"
	"autobuff-monitor/server/internal/notification"
	"autobuff-monitor/server/internal/realtime"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	db, err := database.Open(cfg.DatabaseDSN)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	authService := auth.NewService([]byte(cfg.JWTSecret), cfg.JWTTTL)
	hub := realtime.NewHub()
	notificationService, err := notification.NewService(
		db,
		hub,
		[]byte(cfg.JWTSecret),
		cfg.BarkBaseURL,
		cfg.BarkPublicURL,
		cfg.PublicBaseURL,
		logger,
	)
	if err != nil {
		logger.Error("notification service initialization failed", "error", err)
		os.Exit(1)
	}
	expGainService, err := expgain.NewService(db, hub, logger)
	if err != nil {
		logger.Error("exp gain service initialization failed", "error", err)
		os.Exit(1)
	}
	serviceContext, cancelServices := context.WithCancel(context.Background())
	defer cancelServices()
	notificationService.Start(serviceContext)
	expGainService.Start(serviceContext)
	handler := api.NewServer(cfg, db, authService, hub, notificationService, expGainService, logger).Routes()

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	go func() {
		logger.Info("monitor server started", "address", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	cancelServices()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hub.Close()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
