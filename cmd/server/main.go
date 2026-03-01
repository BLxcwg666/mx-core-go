package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
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
	"github.com/mx-space/core/internal/pkg/proctitle"
	"go.uber.org/zap"
)

func main() {
	bootStartedAt := time.Now()

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

	if cluster.ShouldLogBootstrap() {
		logger.Info("runtime mode",
			zap.String("role", role),
			zap.Bool("cluster", *clusterEnabled),
			zap.Int("cluster_workers", *clusterWorkers),
			zap.Int("worker_id", cluster.WorkerID()),
		)
	}

	opts := cluster.Options{
		Enable:     *clusterEnabled,
		Workers:    *clusterWorkers,
		ListenAddr: ":" + strconv.Itoa(cfg.Port),
	}
	if err := cluster.Run(logger, opts, func() error {
		return runHTTPServer(logger, cfg, *clusterEnabled, bootStartedAt)
	}); err != nil {
		logger.Fatal("server exited with error", zap.Error(err))
	}
}

func runHTTPServer(logger *zap.Logger, cfg *config.AppConfig, clusterEnabled bool, bootStartedAt time.Time) error {
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
		prefix := processLogPrefix()
		serverURL := resolveServerURL(srv.Addr)
		logger.Info("ENV: " + resolveRuntimeEnv(cfg.Env))
		logger.Info(prefix + " Server listen on: " + serverURL)
		logger.Info(prefix + " Admin Local Dashboard: " + serverURL + application.AdminProxyPath())
		logger.Info(prefix + " If you want to debug local dev dashboard on production environment with https domain, you can go to: https://<your-prod-domain>" + application.AdminProxyDevPath())
		logger.Info(prefix + " If you want to debug local dev dashboard on dev environment with same site domain, you can go to: http://localhost:2333" + application.AdminProxyDevPath())
		logger.Info(fmt.Sprintf("Server is up. +%dms", time.Since(bootStartedAt).Milliseconds()))
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

func processLogPrefix() string {
	if cluster.IsWorker() {
		return "[W" + strconv.Itoa(os.Getpid()) + "]"
	}
	return "[P" + strconv.Itoa(os.Getpid()) + "]"
}

func resolveRuntimeEnv(fallbackEnv string) string {
	env := strings.TrimSpace(os.Getenv("NODE_ENV"))
	if env == "" {
		env = strings.TrimSpace(fallbackEnv)
	}
	if env == "" {
		env = "development"
	}
	return env
}

func resolveServerURL(addr string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return "http://localhost"
	}
	if strings.HasPrefix(trimmed, ":") {
		return "http://localhost" + trimmed
	}
	host, port, err := net.SplitHostPort(trimmed)
	if err != nil {
		return "http://" + trimmed
	}
	host = strings.TrimSpace(host)
	switch host {
	case "", "0.0.0.0", "::":
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
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

func setProcessTitle(role string, fallbackEnv string) {
	env := strings.TrimSpace(os.Getenv("NODE_ENV"))
	if env == "" {
		env = strings.TrimSpace(fallbackEnv)
	}
	if env == "" {
		env = "development"
	}

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
