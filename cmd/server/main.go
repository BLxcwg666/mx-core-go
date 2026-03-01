package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mx-space/core/internal/app"
	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/database"
	"github.com/mx-space/core/internal/pkg/cluster"
	"github.com/mx-space/core/internal/pkg/nativelog"
	"github.com/mx-space/core/internal/pkg/prettylog"
	"github.com/mx-space/core/internal/pkg/proctitle"
	"go.uber.org/zap"
)

func main() {
	configPath := flag.String("config", config.DefaultConfigPath, "Path to YAML config file")
	clusterEnabled := flag.Bool("cluster", boolEnv("CLUSTER", false), "Enable cluster mode")
	clusterWorkers := flag.Int("cluster_workers", intEnv("CLUSTER_WORKERS", 0), "Cluster worker count")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fallbackLogger, _ := zap.NewProduction()
		fallbackLogger.Fatal("failed to load config", zap.String("path", *configPath), zap.Error(err))
	}
	_ = os.Setenv(nativelog.EnvLogDir, cfg.LogDir())
	if sizeMB, ok := cfg.LogRotateSizeMB(); ok {
		_ = os.Setenv(nativelog.EnvLogRotateSizeMB, strconv.Itoa(sizeMB))
	}
	if keep, ok := cfg.LogRotateKeepCount(); ok {
		_ = os.Setenv(nativelog.EnvLogRotateKeep, strconv.Itoa(keep))
	}

	logger, err := nativelog.NewZapLogger()
	if err != nil {
		logger, _ = zap.NewProduction()
		if cluster.ShouldLogBootstrap() {
			logger.Warn("native log pipeline unavailable, fallback to zap production logger", zap.Error(err))
		}
	}
	defer logger.Sync()

	if !*clusterEnabled && cfg.Cluster {
		*clusterEnabled = true
	}
	if *clusterWorkers <= 0 && cfg.ClusterWorkers > 0 {
		*clusterWorkers = cfg.ClusterWorkers
	}

	if shouldBootstrapSchema() {
		if err := database.EnsureSchema(cfg); err != nil {
			logger.Fatal("database schema bootstrap failed", zap.Error(err))
		}
	}

	role := resolveRole(*clusterEnabled)
	setProcessTitle(role, cfg.Env)

	// Match mx-core: log ENV on startup.
	if cluster.ShouldLogBootstrap() {
		logger.Info("ENV: " + resolveEnv(cfg.Env))
	}

	opts := cluster.Options{
		Enable:     *clusterEnabled,
		Workers:    *clusterWorkers,
		ListenAddr: ":" + strconv.Itoa(cfg.Port),
	}
	if err := cluster.Run(logger, opts, func() error {
		return runHTTPServer(logger, cfg, *clusterEnabled)
	}); err != nil {
		logger.Fatal("server exited with error", zap.Error(err))
	}
}

func runHTTPServer(logger *zap.Logger, cfg *config.AppConfig, clusterEnabled bool) error {
	application, err := app.New(logger, cfg)
	if err != nil {
		return fmt.Errorf("initialize app: %w", err)
	}

	srv := &http.Server{
		Addr:    application.Addr(),
		Handler: application.Router(),
	}

	useReusePort := clusterEnabled && cluster.IsWorker() && cluster.WorkerListenAddr() == ""
	listenAddr := srv.Addr
	if cluster.IsWorker() {
		if workerAddr := cluster.WorkerListenAddr(); workerAddr != "" {
			listenAddr = workerAddr
			srv.Addr = workerAddr
		}
	}
	listener, err := cluster.ListenTCP(listenAddr, useReusePort)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}

	if cluster.ShouldLogBootstrap() {
		pid := os.Getpid()
		prefix := "P"
		if cluster.IsWorker() {
			prefix = "W"
		}
		url := "http://localhost" + srv.Addr

		logger.Info(
			fmt.Sprintf("[%s%d] Server listen on: %s", prefix, pid, url),
			prettylog.SuccessField(),
		)
		logger.Info(
			fmt.Sprintf("[%s%d] Admin Local Dashboard: %s%s", prefix, pid, url, application.AdminProxyPath()),
			prettylog.SuccessField(),
		)
		logger.Info(
			fmt.Sprintf("[%s%d] Admin Dev Dashboard Proxy: %s%s", prefix, pid, url, application.AdminProxyDevPath()),
		)
		logger.Info(fmt.Sprintf("Server is up. %s", prettylog.Yellow(fmt.Sprintf("+%dms", prettylog.UptimeMs()))))
	}

	serveErrCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrCh <- err
			return
		}
		serveErrCh <- nil
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case err := <-serveErrCh:
		return err
	case <-quit:
		if cluster.ShouldLogBootstrap() {
			logger.Info("shutting down server...")
		}
		application.Shutdown()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			return fmt.Errorf("forced shutdown: %w", err)
		}
		_ = <-serveErrCh
		if cluster.ShouldLogBootstrap() {
			logger.Info("server exited")
		}
		return nil
	}
}

func resolveRole(clusterEnabled bool) string {
	if cluster.IsWorker() {
		return cluster.RoleWorker
	}
	if clusterEnabled {
		return cluster.RoleMaster
	}
	return "single"
}

func resolveEnv(fallbackEnv string) string {
	env := strings.TrimSpace(os.Getenv("NODE_ENV"))
	if env == "" {
		env = strings.TrimSpace(fallbackEnv)
	}
	if env == "" {
		env = "development"
	}
	return env
}

func setProcessTitle(role string, fallbackEnv string) {
	env := resolveEnv(fallbackEnv)

	clusterRole := "master"
	if role == cluster.RoleWorker {
		clusterRole = "worker"
	}
	title := fmt.Sprintf("Mix Space (%s) - %s", clusterRole, env)
	_ = proctitle.Set(title)
}

func boolEnv(key string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func intEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func shouldBootstrapSchema() bool {
	if cluster.IsWorker() {
		return false
	}
	if mainCluster, ok := cluster.IsMainClusterInstance(); ok {
		return mainCluster
	}
	return true
}
