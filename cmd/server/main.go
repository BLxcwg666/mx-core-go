package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mx-space/core/internal/app"
	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/pkg/nativelog"
	"go.uber.org/zap"
)

func main() {
	configPath := flag.String("config", config.DefaultConfigPath, "Path to YAML config file")
	flag.Parse()

	logger, err := nativelog.NewZapLogger()
	if err != nil {
		logger, _ = zap.NewProduction()
		logger.Warn("native log pipeline unavailable, fallback to zap production logger", zap.Error(err))
	}
	defer logger.Sync()

	application, err := app.New(logger, *configPath)
	if err != nil {
		logger.Fatal("failed to initialize app", zap.Error(err))
	}

	srv := &http.Server{
		Addr:    application.Addr(),
		Handler: application.Router(),
	}

	go func() {
		logger.Info("server starting", zap.String("addr", srv.Addr))
		logger.Info("admin local dashboard", zap.String("url", "http://localhost"+srv.Addr+application.AdminProxyPath()))
		logger.Info("admin dev dashboard proxy", zap.String("url", "http://localhost"+srv.Addr+application.AdminProxyDevPath()))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down server...")
	application.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("forced shutdown", zap.Error(err))
	}
	logger.Info("server exited")
}
