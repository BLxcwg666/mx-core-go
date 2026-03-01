//go:build windows

package cluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"
)

type workerExit struct {
	id   int
	pid  int
	code int
}

// Run starts cluster mode when enabled; otherwise runs workerMain directly.
func Run(logger *zap.Logger, opts Options, workerMain func() error) error {
	if workerMain == nil {
		return errors.New("workerMain is nil")
	}
	if err := validateOptions(opts); err != nil {
		return err
	}

	if !opts.Enable {
		return workerMain()
	}
	if IsWorker() {
		return workerMain()
	}

	return runMasterWindows(logger, opts.Workers, opts.ListenAddr)
}

func runMasterWindows(logger *zap.Logger, requestedWorkers int, listenAddr string) error {
	listenAddr = strings.TrimSpace(listenAddr)
	if listenAddr == "" {
		return errors.New("cluster listen address is required on windows")
	}

	host, port, err := parseListenAddr(listenAddr)
	if err != nil {
		return err
	}
	workerHost := host
	if workerHost == "" || workerHost == "0.0.0.0" || workerHost == "::" {
		workerHost = "127.0.0.1"
	}

	workerCount := normalizedWorkers(requestedWorkers)
	if logger != nil {
		logger.Info(fmt.Sprintf("Primary server started on %d", os.Getpid()))
		logger.Info(fmt.Sprintf("CPU:%d", runtime.NumCPU()))
		logger.Info("cluster mode enabled", zap.Int("workers", workerCount), zap.String("addr", listenAddr))
	}

	exitCh := make(chan workerExit, workerCount*2)
	workers := make(map[int]*exec.Cmd, workerCount)
	workerTargets := make(map[int]string, workerCount)

	startWorker := func(id int) error {
		addr := internalWorkerAddr(workerHost, port, id)
		cmd, err := spawnWorkerWindows(id, addr)
		if err != nil {
			return err
		}
		workers[id] = cmd
		workerTargets[id] = "http://" + addr

		if logger != nil {
			logger.Info(fmt.Sprintf("Worker %d is online", cmd.Process.Pid), zap.Int("worker_id", id), zap.String("addr", addr))
		}

		go func(workerID int, processID int, c *exec.Cmd) {
			err := c.Wait()
			exitCh <- workerExit{
				id:   workerID,
				pid:  processID,
				code: exitCode(err),
			}
		}(id, cmd.Process.Pid, cmd)

		return nil
	}

	for i := 1; i <= workerCount; i++ {
		if err := startWorker(i); err != nil {
			killAllWorkers(workers, nil)
			return err
		}
	}

	targetPicker, err := newRoundRobinPicker(workerTargets)
	if err != nil {
		killAllWorkers(workers, nil)
		return err
	}
	proxyHandler := buildProxyHandler(targetPicker, logger)

	srv := &http.Server{
		Addr:    listenAddr,
		Handler: proxyHandler,
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		killAllWorkers(workers, nil)
		return fmt.Errorf("master listen %s: %w", listenAddr, err)
	}
	serveErrCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrCh <- err
			return
		}
		serveErrCh <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	stopping := false
	var killTimer <-chan time.Time

	for len(workers) > 0 {
		select {
		case err := <-serveErrCh:
			if err != nil {
				stopping = true
				interruptAllWorkers(workers, logger)
				killAllWorkers(workers, logger)
				return err
			}
			if stopping {
				continue
			}
			return errors.New("proxy server exited unexpectedly")

		case sig := <-sigCh:
			if stopping {
				continue
			}
			stopping = true
			if logger != nil {
				logger.Info("Cluster shutting down...", zap.String("signal", sig.String()))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			_ = srv.Shutdown(ctx)
			cancel()
			interruptAllWorkers(workers, logger)
			killTimer = time.After(8 * time.Second)

		case <-killTimer:
			killAllWorkers(workers, logger)
			killTimer = nil

		case ex := <-exitCh:
			cmd, ok := workers[ex.id]
			if !ok || cmd == nil || cmd.Process == nil {
				continue
			}
			if cmd.Process.Pid != ex.pid {
				continue
			}
			delete(workers, ex.id)
			delete(workerTargets, ex.id)
			targetPicker.Reset(workerTargets)

			if logger != nil && ex.code != 0 {
				logger.Warn("worker exited with non-zero code", zap.Int("worker_id", ex.id), zap.Int("pid", ex.pid), zap.Int("code", ex.code))
			}

			if stopping {
				continue
			}

			if ex.code != 0 {
				if logger != nil {
					logger.Warn(fmt.Sprintf("Worker %d died. Restarting", ex.pid), zap.Int("worker_id", ex.id))
				}
				if err := startWorker(ex.id); err != nil {
					return err
				}
				targetPicker.Reset(workerTargets)
			}
		}
	}

	if logger != nil {
		logger.Info("Primary server exited")
	}
	return nil
}

func spawnWorkerWindows(id int, workerAddr string) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}

	args := append([]string{}, os.Args[1:]...)
	cmd := exec.Command(executable, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = workerEnvWindows(os.Environ(), id, workerAddr)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start worker %d: %w", id, err)
	}
	return cmd, nil
}

func workerEnvWindows(base []string, id int, workerAddr string) []string {
	env := make([]string, 0, len(base)+3)
	for _, kv := range base {
		if hasEnvKey(kv, EnvRole) || hasEnvKey(kv, EnvWorkerID) || hasEnvKey(kv, EnvWorkerAddr) {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, EnvRole+"="+RoleWorker)
	env = append(env, EnvWorkerID+"="+strconv.Itoa(id))
	env = append(env, EnvWorkerAddr+"="+workerAddr)
	return env
}

func hasEnvKey(kv, key string) bool {
	if len(kv) < len(key)+1 {
		return false
	}
	return kv[:len(key)] == key && kv[len(key)] == '='
}

func interruptAllWorkers(workers map[int]*exec.Cmd, logger *zap.Logger) {
	for id, cmd := range workers {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		if err := cmd.Process.Signal(os.Interrupt); err != nil && logger != nil {
			logger.Warn("failed to send shutdown signal to worker", zap.Int("worker_id", id), zap.Int("pid", cmd.Process.Pid), zap.Error(err))
		}
		if logger != nil {
			logger.Info("sent shutdown signal to worker", zap.Int("worker_id", id), zap.Int("pid", cmd.Process.Pid))
		}
	}
}

func killAllWorkers(workers map[int]*exec.Cmd, logger *zap.Logger) {
	for id, cmd := range workers {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		if err := cmd.Process.Kill(); err != nil && logger != nil {
			logger.Warn("failed to kill worker", zap.Int("worker_id", id), zap.Int("pid", cmd.Process.Pid), zap.Error(err))
			continue
		}
		if logger != nil {
			logger.Warn("worker force killed", zap.Int("worker_id", id), zap.Int("pid", cmd.Process.Pid))
		}
	}
}

func normalizedWorkers(requested int) int {
	cpus := runtime.NumCPU()
	if requested <= 0 {
		return cpus
	}
	if requested > cpus {
		return cpus
	}
	return requested
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func parseListenAddr(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			host = ""
			portStr = strings.TrimPrefix(addr, ":")
		} else {
			return "", 0, fmt.Errorf("invalid listen address %q: %w", addr, err)
		}
	}
	port, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid listen port in %q", addr)
	}
	return host, port, nil
}

func internalWorkerAddr(host string, basePort int, workerID int) string {
	return net.JoinHostPort(host, strconv.Itoa(basePort+100+workerID))
}

type roundRobinPicker struct {
	mu      sync.RWMutex
	targets []*url.URL
	idx     uint64
}

func newRoundRobinPicker(targets map[int]string) (*roundRobinPicker, error) {
	p := &roundRobinPicker{}
	if err := p.Reset(targets); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *roundRobinPicker) Reset(targets map[int]string) error {
	next := make([]*url.URL, 0, len(targets))
	for id, target := range targets {
		u, err := url.Parse(target)
		if err != nil {
			return fmt.Errorf("parse target for worker %d: %w", id, err)
		}
		next = append(next, u)
	}
	if len(next) == 0 {
		return errors.New("no available worker targets")
	}
	p.mu.Lock()
	p.targets = next
	p.mu.Unlock()
	atomic.StoreUint64(&p.idx, 0)
	return nil
}

func (p *roundRobinPicker) Next() *url.URL {
	p.mu.RLock()
	targets := p.targets
	p.mu.RUnlock()
	if len(targets) == 0 {
		return nil
	}
	n := atomic.AddUint64(&p.idx, 1)
	return targets[(n-1)%uint64(len(targets))]
}

func buildProxyHandler(picker *roundRobinPicker, logger *zap.Logger) http.Handler {
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			target := picker.Next()
			if target == nil {
				return
			}
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if logger != nil {
				logger.Warn("proxy request failed", zap.String("path", r.URL.Path), zap.Error(err))
			}
			http.Error(w, "failed to proxy cluster! please check server console", http.StatusBadGateway)
		},
	}

	return proxy
}
