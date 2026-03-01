package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/modules/storage/backup"
	"github.com/mx-space/core/internal/modules/storage/file"
	"github.com/mx-space/core/internal/pkg/cluster"
	jwtpkg "github.com/mx-space/core/internal/pkg/jwt"
	"github.com/mx-space/core/internal/pkg/nativelog"
	"go.uber.org/zap"
)

func applyRuntimeSettings(cfg *config.AppConfig, logger *zap.Logger) error {
	_ = os.Setenv(nativelog.EnvLogDir, cfg.LogDir())
	if sizeMB, ok := cfg.LogRotateSizeMB(); ok {
		_ = os.Setenv(nativelog.EnvLogRotateSizeMB, strconv.Itoa(sizeMB))
	}
	if keep, ok := cfg.LogRotateKeepCount(); ok {
		_ = os.Setenv(nativelog.EnvLogRotateKeep, strconv.Itoa(keep))
	}
	_ = os.Setenv(backup.EnvBackupDir, cfg.BackupDir())
	_ = os.Setenv(file.EnvStaticDir, cfg.StaticDir())

	if secret := strings.TrimSpace(cfg.JWTSecret); secret != "" {
		jwtpkg.SetSecret(secret)
	} else {
		if cluster.ShouldLogBootstrap() {
			logger.Warn("jwt_secret is empty, using built-in default secret")
		}
	}

	tz := strings.TrimSpace(cfg.Timezone)
	if tz == "" {
		return nil
	}
	loc, err := parseTimezoneLocation(tz)
	if err != nil {
		return fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	time.Local = loc
	_ = os.Setenv("TZ", tz)
	return nil
}

func parseTimezoneLocation(raw string) (*time.Location, error) {
	tz := strings.TrimSpace(raw)
	if tz == "" {
		return time.Local, nil
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc, nil
	}
	if len(tz) == 6 && (tz[0] == '+' || tz[0] == '-') && tz[3] == ':' {
		h, errH := strconv.Atoi(tz[1:3])
		m, errM := strconv.Atoi(tz[4:6])
		if errH == nil && errM == nil && h <= 23 && m <= 59 {
			offset := h*3600 + m*60
			if tz[0] == '-' {
				offset = -offset
			}
			return time.FixedZone(tz, offset), nil
		}
	}
	return nil, fmt.Errorf("expect IANA zone (e.g. Asia/Shanghai) or UTC offset (e.g. +08:00)")
}

func humanizeDuration(d time.Duration) string {
	if d < time.Minute {
		return d.Truncate(time.Second).String()
	}
	if d < time.Hour {
		return d.Truncate(time.Minute).String()
	}
	if d < 24*time.Hour {
		return d.Truncate(time.Hour).String()
	}
	return d.Truncate(24 * time.Hour).String()
}
