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

	var tokenKey []byte
	if tokenKeyHex := strings.TrimSpace(os.Getenv("TOKEN_ENCRYPTION_KEY")); tokenKeyHex != "" {
		key, err := hex.DecodeString(tokenKeyHex)
		if err != nil {
			return fmt.Errorf("TOKEN_ENCRYPTION_KEY: invalid hex: %w", err)
		}
		if len(key) != 32 {
			return fmt.Errorf("TOKEN_ENCRYPTION_KEY: got %d bytes, need 32 (64 hex chars)", len(key))
		}
		tokenKey = key
	} else {
		logger.Warn("TOKEN_ENCRYPTION_KEY not set — Questrade integration and 2FA disabled")
	}

	userStore := sqlite.NewUserStore(db, tokenKey)
	sessionStore := sqlite.NewSessionStore(db)
	accountStore := sqlite.NewAccountStore(db)
	securityStore := sqlite.NewSecurityStore(db)
	txStore := sqlite.NewTransactionStore(db)
	auditStore := sqlite.NewAuditStore(db)
	fxStore := sqlite.NewFXStore(db)

	var qtTokenStore questrade.Store
	if tokenKey != nil {
		qtTokenStore = sqlite.NewQTokenStore(db, tokenKey)
	}

	distStore := sqlite.NewDistributionStore(db)
	acbSvc := service.NewACBService(txStore)
	bocSvc := service.NewBOCFetcher(fxStore)
	gainsSvc := service.NewGainsService(txStore, securityStore)
	rocSvc := service.NewROCService(txStore, distStore, securityStore)

	secureCookie := strings.ToLower(strings.TrimSpace(os.Getenv("SECURE_COOKIES"))) == "true"

	h, err := handler.New(&handler.Config{
		Accounts:     accountStore,
		Securities:   securityStore,
		Transactions: txStore,
		Audits:       auditStore,
		QTTokens:     qtTokenStore,
		Users:        userStore,
		Sessions:     sessionStore,
		BOCSvc:       bocSvc,
		ACBSvc:       acbSvc,
		GainsSvc:     gainsSvc,
		ROCSvc:       rocSvc,
		EncKey:       tokenKey,
		SecureCookie: secureCookie,
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
		Handler:      handler.RequestLogger(logger)(h.SetupGate(mux)),
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
