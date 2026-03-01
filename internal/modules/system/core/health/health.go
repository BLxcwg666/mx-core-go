package health

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	"github.com/mx-space/core/internal/pkg/cron"
	pkgmail "github.com/mx-space/core/internal/pkg/mail"
	"github.com/mx-space/core/internal/pkg/nativelog"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
)

type logItem struct {
	Size     string `json:"size"`
	Filename string `json:"filename"`
	Type     string `json:"type"`
	Index    int    `json:"index"`
	Created  int64  `json:"created"`
}

func RegisterRoutes(rg *gin.RouterGroup, db *gorm.DB, sched *cron.Scheduler, cfgSvc *appconfigs.Service, authMW gin.HandlerFunc) {
	rg.GET("/health", func(c *gin.Context) {
		sqlDB, err := db.DB()
		dbOK := err == nil && sqlDB.Ping() == nil

		status := "ok"
		code := http.StatusOK
		if !dbOK {
			status = "degraded"
			code = http.StatusServiceUnavailable
		}

		c.JSON(code, gin.H{
			"status":   status,
			"database": dbOK,
		})
	})

	adminHealth := rg.Group("/health", authMW)
	cronGroup := adminHealth.Group("/cron")
	{
		cronGroup.GET("", func(c *gin.Context) {
			items := sched.List()
			byName := make(map[string]cron.ListItem, len(items))
			for _, item := range items {
				byName[item.Name] = item
			}
			response.OK(c, byName)
		})

		cronGroup.POST("/run/:name", func(c *gin.Context) {
			if err := sched.Run(c.Request.Context(), c.Param("name")); err != nil {
				response.NotFoundMsg(c, err.Error())
				return
			}
			response.OK(c, gin.H{"message": "job triggered"})
		})

		cronGroup.GET("/task/:name", func(c *gin.Context) {
			result, err := sched.GetTask(c.Param("name"))
			if err != nil {
				response.NotFoundMsg(c, err.Error())
				return
			}
			response.OK(c, result)
		})
	}

	adminHealth.GET("/email/test", func(c *gin.Context) {
		cfg, err := cfgSvc.Get()
		if err != nil {
			response.InternalError(c, err)
			return
		}
		if !cfg.MailOptions.Enable {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "mail is not enabled"})
			return
		}

		mailCfg := pkgmail.Config{
			Enable: cfg.MailOptions.Enable,
			From:   cfg.MailOptions.From,
		}
		if cfg.MailOptions.SMTP != nil {
			mailCfg.Host = cfg.MailOptions.SMTP.Options.Host
			mailCfg.Port = cfg.MailOptions.SMTP.Options.Port
			mailCfg.User = cfg.MailOptions.SMTP.User
			mailCfg.Pass = cfg.MailOptions.SMTP.Pass
		}
		if cfg.MailOptions.Resend != nil && cfg.MailOptions.Resend.APIKey != "" {
			mailCfg.UseResend = true
			mailCfg.ResendKey = cfg.MailOptions.Resend.APIKey
		}

		var owner struct{ Mail string }
		db.Table("users").Select("mail").Scan(&owner)
		if owner.Mail == "" {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "owner email not set"})
			return
		}

		sender := pkgmail.New(mailCfg)
		if err := sender.Send(pkgmail.Message{
			To:      []string{owner.Mail},
			Subject: "Mix Space 邮件测试",
			HTML:    "<h1>邮件配置测试成功！</h1><p>如果您收到此邮件，说明邮件服务已正确配置。</p>",
		}); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}

		response.OK(c, gin.H{"ok": true})
	})

	logGroup := adminHealth.Group("/log")
	{
		logGroup.GET("/list/:type", func(c *gin.Context) {
			logType := c.Param("type")
			logDir, err := resolveLogDir(logType)
			if err != nil {
				response.BadRequest(c, err.Error())
				return
			}

			entries, err := os.ReadDir(logDir)
			if err != nil {
				if logType == "native" && errors.Is(err, os.ErrNotExist) {
					response.OK(c, []logItem{})
					return
				}
				response.BadRequest(c, "log dir not exists")
				return
			}

			items := make([]logItem, 0, len(entries))
			nativeIndex := 0
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}

				filename := entry.Name()
				if logType == "pm2" {
					if !strings.HasPrefix(filename, "mx-server-") || !strings.HasSuffix(filename, ".log") {
						continue
					}
				}

				info, err := entry.Info()
				if err != nil {
					continue
				}

				itemType := "log"
				itemIndex := nativeIndex
				if logType == "pm2" {
					itemType, itemIndex = parsePM2LogMeta(filename)
				} else {
					nativeIndex++
				}

				items = append(items, logItem{
					Size:     formatByteSize(info.Size()),
					Filename: filename,
					Type:     itemType,
					Index:    itemIndex,
					Created:  info.ModTime().UnixMilli(),
				})
			}

			sort.Slice(items, func(i, j int) bool {
				return items[i].Created > items[j].Created
			})
			response.OK(c, items)
		})

		logGroup.GET("/:type", func(c *gin.Context) {
			logType := c.Param("type")
			logDir, err := resolveLogDir(logType)
			if err != nil {
				response.BadRequest(c, err.Error())
				return
			}
			if _, err := os.Stat(logDir); err != nil {
				response.BadRequest(c, "log dir not exists")
				return
			}

			filename := strings.TrimSpace(c.Query("filename"))
			if filename == "" {
				if logType == "native" {
					response.UnprocessableEntity(c, "filename must be string")
					return
				}

				pm2Type := strings.TrimSpace(c.DefaultQuery("type", "out"))
				if pm2Type != "out" && pm2Type != "error" {
					pm2Type = "out"
				}
				index := 0
				if raw := strings.TrimSpace(c.Query("index")); raw != "" {
					if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
						index = parsed
					}
				}
				if index == 0 {
					filename = fmt.Sprintf("mx-server-%s.log", pm2Type)
				} else {
					filename = fmt.Sprintf("mx-server-%s-%d.log", pm2Type, index)
				}
			}

			filename = filepath.Base(filename)
			if filename == "." || filename == string(filepath.Separator) {
				response.UnprocessableEntity(c, "filename must be string")
				return
			}

			logPath := filepath.Join(logDir, filename)
			data, err := os.ReadFile(logPath)
			if err != nil {
				response.BadRequest(c, "log file not exists")
				return
			}

			c.Data(http.StatusOK, "text/plain; charset=utf-8", data)
		})

		logGroup.DELETE("/:type", func(c *gin.Context) {
			logType := c.Param("type")
			logDir, err := resolveLogDir(logType)
			if err != nil {
				response.BadRequest(c, err.Error())
				return
			}

			filename := strings.TrimSpace(c.Query("filename"))
			if filename == "" {
				response.UnprocessableEntity(c, "filename must be string")
				return
			}

			filename = filepath.Base(filename)
			if filename == "." || filename == string(filepath.Separator) {
				response.UnprocessableEntity(c, "filename must be string")
				return
			}

			targetPath := filepath.Join(logDir, filename)
			switch logType {
			case "native":
				todayPath := filepath.Join(logDir, todayNativeLogFilename())
				if strings.HasSuffix(strings.ToLower(targetPath), "error.log") || samePath(targetPath, todayPath) {
					if err := os.WriteFile(targetPath, []byte(""), 0o644); err != nil && !errors.Is(err, os.ErrNotExist) {
						response.InternalError(c, err)
						return
					}
				} else if err := os.Remove(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
					response.InternalError(c, err)
					return
				}
			case "pm2":
				if _, err := os.Stat(logDir); err != nil {
					response.BadRequest(c, "log dir not exists")
					return
				}
				if err := os.Remove(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
					response.InternalError(c, err)
					return
				}
			default:
				response.BadRequest(c, "invalid log type")
				return
			}

			response.NoContent(c)
		})
	}
}

func resolveLogDir(logType string) (string, error) {
	switch logType {
	case "native":
		return resolveNativeLogDir(), nil
	case "pm2":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".pm2", "logs"), nil
	default:
		return "", fmt.Errorf("invalid log type")
	}
}

func resolveNativeLogDir() string {
	return nativelog.ResolveDir()
}

func parsePM2LogMeta(filename string) (string, int) {
	name := strings.TrimSuffix(filename, ".log")
	parts := strings.Split(name, "-")
	if len(parts) < 3 {
		return "out", 0
	}

	logType := parts[2]
	index := 0
	if len(parts) > 3 {
		if parsed, err := strconv.Atoi(parts[3]); err == nil && parsed >= 0 {
			index = parsed
		}
	}
	return logType, index
}

func formatByteSize(size int64) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.2f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

func todayNativeLogFilename() string {
	return nativelog.TodayFilename(time.Now())
}

func samePath(a, b string) bool {
	left := filepath.Clean(a)
	right := filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
