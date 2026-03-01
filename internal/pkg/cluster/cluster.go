package cluster

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

const (
	EnvRole       = "MX_CLUSTER_ROLE"
	EnvWorkerID   = "MX_CLUSTER_WORKER_ID"
	EnvWorkerAddr = "MX_CLUSTER_WORKER_ADDR"

	RoleMaster = "master"
	RoleWorker = "worker"
)

type Options struct {
	Enable     bool
	Workers    int
	ListenAddr string
}

func IsWorker() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(EnvRole)), RoleWorker)
}

func WorkerID() int {
	raw := strings.TrimSpace(os.Getenv(EnvWorkerID))
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 {
		return 0
	}
	return v
}

func WorkerListenAddr() string {
	return strings.TrimSpace(os.Getenv(EnvWorkerAddr))
}

func IsMainClusterInstance() (bool, bool) {
	for _, key := range []string{"NODE_APP_INSTANCE", "pm_id", "INSTANCE_ID"} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		v, err := strconv.Atoi(raw)
		if err != nil {
			return false, true
		}
		return v == 0, true
	}
	return false, false
}

// ShouldRunCron keeps cron jobs single-run across clustered workers.
func ShouldRunCron() bool {
	if IsWorker() {
		return WorkerID() == 1
	}
	if mainCluster, ok := IsMainClusterInstance(); ok {
		return mainCluster
	}
	return true
}

// ShouldLogBootstrap keeps startup/runtime logs from being printed N times.
func ShouldLogBootstrap() bool {
	if IsWorker() {
		return false
	}
	if mainCluster, ok := IsMainClusterInstance(); ok {
		return mainCluster
	}
	return true
}

// ShouldLogDevDiagnostics keeps dev-only framework logs visible once in cluster mode.
func ShouldLogDevDiagnostics() bool {
	if IsWorker() {
		return WorkerID() == 1
	}
	if mainCluster, ok := IsMainClusterInstance(); ok {
		return mainCluster
	}
	return true
}

func validateOptions(opts Options) error {
	if opts.Enable && opts.Workers < 0 {
		return errors.New("cluster workers must be >= 0")
	}
	return nil
}
