package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gordcurrie/pacioli/internal/handler"
	"github.com/gordcurrie/pacioli/internal/questrade"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/sqlite"
	"github.com/gordcurrie/pacioli/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	dsn := envOrDefault("DATABASE_DSN", "pacioli.db")
	db, err := sqlite.Open(dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	userStore := sqlite.NewUserStore(db)
	userID, err := userStore.EnsureDefault(context.Background(), envOrDefault("USER_EMAIL", "admin@pacioli.local"))
	if err != nil {
		return fmt.Errorf("ensure default user: %w", err)
	}

	accountStore := sqlite.NewAccountStore(db)
	securityStore := sqlite.NewSecurityStore(db)
	txStore := sqlite.NewTransactionStore(db)
	auditStore := sqlite.NewAuditStore(db)
	fxStore := sqlite.NewFXStore(db)

	var qtTokenStore questrade.Store
	if tokenKeyHex := strings.TrimSpace(os.Getenv("TOKEN_ENCRYPTION_KEY")); tokenKeyHex != "" {
		tokenKey, err := hex.DecodeString(tokenKeyHex)
		if err != nil {
			return fmt.Errorf("TOKEN_ENCRYPTION_KEY: invalid hex: %w", err)
		}
		if len(tokenKey) != 32 {
			return fmt.Errorf("TOKEN_ENCRYPTION_KEY: got %d bytes, need 32 (64 hex chars)", len(tokenKey))
		}
		qtTokenStore = sqlite.NewQTokenStore(db, tokenKey)
	} else {
		logger.Warn("TOKEN_ENCRYPTION_KEY not set — Questrade integration disabled")
	}

	acbSvc := service.NewACBService(txStore)
	bocSvc := service.NewBOCFetcher(fxStore)

	h, err := handler.New(&handler.Config{
		Accounts:     accountStore,
		Securities:   securityStore,
		Transactions: txStore,
		Audits:       auditStore,
		QTTokens:     qtTokenStore,
		BOCSvc:       bocSvc,
		ACBSvc:       acbSvc,
		UserID:       userID,
		Logger:       logger,
		TemplateFS:   web.Templates,
	})
	if err != nil {
		return fmt.Errorf("init handlers: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h.Routes(mux)

	addr := envOrDefault("ADDR", ":8080")
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler.RequestLogger(logger)(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	srvErr := make(chan error, 1)
	go func() {
		logger.Info("server starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-srvErr:
		return fmt.Errorf("server: %w", err)
	case <-quit:
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	logger.Info("server stopped")
	return nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
