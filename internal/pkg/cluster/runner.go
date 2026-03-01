//go:build !windows

package cluster

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
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

	return runMaster(logger, opts.Workers)
}

func runMaster(logger *zap.Logger, requestedWorkers int) error {
	workerCount := normalizedWorkers(requestedWorkers)
	if logger != nil {
		logger.Info("cluster mode enabled",
			zap.Int("master_pid", os.Getpid()),
			zap.Int("workers", workerCount),
			zap.Int("cpu", runtime.NumCPU()),
		)
	}

	exitCh := make(chan workerExit, workerCount*2)
	workers := make(map[int]*exec.Cmd, workerCount)

	startWorker := func(id int) error {
		cmd, err := spawnWorker(id)
		if err != nil {
			return err
		}
		workers[id] = cmd

		if logger != nil {
			logger.Info("worker started", zap.Int("worker_id", id), zap.Int("pid", cmd.Process.Pid))
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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	stopping := false
	var killTimer <-chan time.Time

	for len(workers) > 0 {
		select {
		case sig := <-sigCh:
			if stopping {
				continue
			}
			stopping = true
			if logger != nil {
				logger.Info("cluster shutting down", zap.String("signal", sig.String()))
			}
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

			if logger != nil {
				if ex.code == 0 {
					logger.Info("worker exited", zap.Int("worker_id", ex.id), zap.Int("pid", ex.pid), zap.Int("code", ex.code))
				} else {
					logger.Warn("worker exited", zap.Int("worker_id", ex.id), zap.Int("pid", ex.pid), zap.Int("code", ex.code))
				}
			}

			if stopping {
				continue
			}

			if ex.code != 0 {
				if logger != nil {
					logger.Warn("worker crashed, restarting", zap.Int("worker_id", ex.id))
				}
				if err := startWorker(ex.id); err != nil {
					return err
				}
			}
		}
	}

	if logger != nil {
		logger.Info("cluster master exited")
	}
	return nil
}

func spawnWorker(id int) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}

	args := append([]string{}, os.Args[1:]...)
	cmd := exec.Command(executable, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = workerEnv(os.Environ(), id)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start worker %d: %w", id, err)
	}
	return cmd, nil
}

func workerEnv(base []string, id int) []string {
	env := make([]string, 0, len(base)+2)
	for _, kv := range base {
		if hasEnvKey(kv, EnvRole) || hasEnvKey(kv, EnvWorkerID) {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, EnvRole+"="+RoleWorker)
	env = append(env, EnvWorkerID+"="+strconv.Itoa(id))
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
