package app

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/config"
)

func applyTrustedProxySettings(router *gin.Engine, cfg *config.AppConfig) error {
	if router == nil {
		return nil
	}

	if cfg == nil || !cfg.TrustedProxy.Enable {
		router.TrustedPlatform = ""
		router.ForwardedByClientIP = false
		router.RemoteIPHeaders = nil
		return router.SetTrustedProxies(nil)
	}

	if len(cfg.TrustedProxy.Headers) == 0 {
		return errors.New("trusted proxy headers are empty")
	}
	if len(cfg.TrustedProxy.Proxies) == 0 {
		return errors.New("trusted proxy proxies are empty")
	}

	router.TrustedPlatform = ""
	router.ForwardedByClientIP = true
	router.RemoteIPHeaders = append([]string(nil), cfg.TrustedProxy.Headers...)
	return router.SetTrustedProxies(append([]string(nil), cfg.TrustedProxy.Proxies...))
}
