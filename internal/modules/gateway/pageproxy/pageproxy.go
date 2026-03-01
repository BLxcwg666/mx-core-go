package pageproxy

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	appcfg "github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/modules/system/core/configs"
)

// Handler serves locally bundled admin dashboard assets under /proxy/*.
type Handler struct {
	cfgSvc    *configs.Service
	runtime   *appcfg.AppConfig
	adminPath string
}

func NewHandler(cfgSvc *configs.Service, runtime *appcfg.AppConfig) *Handler {
	adminPath := ""
	if runtime != nil {
		adminPath = strings.TrimSpace(runtime.AdminAssetPath())
	}
	if adminPath == "" {
		adminPath = strings.TrimSpace(os.Getenv("MX_ADMIN_ASSET_PATH"))
	}
	if adminPath == "" {
		adminPath = "admin"
	}
	adminPath = filepath.Clean(adminPath)
	return &Handler{
		cfgSvc:    cfgSvc,
		runtime:   runtime,
		adminPath: adminPath,
	}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	base := h.adminProxyBasePath()
	rg.GET(base, h.getLocalBundledAdmin)
	rg.GET(base+"/dev-proxy", h.proxyLocalDev)
	rg.GET(base+"/assets/*filepath", h.proxyAssetRoute)
	rg.GET("/proxy/:legacyRoot/*filepath", h.proxyLegacyAssetRoute)
}

func (h *Handler) getLocalBundledAdmin(c *gin.Context) {
	canAccess, err := h.checkCanAccessAdminProxy()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if !canAccess {
		c.JSON(http.StatusForbidden, gin.H{"message": "admin proxy not enabled"})
		return
	}

	if c.Query("log") != "" {
		c.String(http.StatusOK, "")
		return
	}

	entryPath := filepath.Join(h.adminPath, "index.html")
	content, err := os.ReadFile(entryPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{
				"message": "local admin assets not found, set mx-admin in config.yml (or MX_ADMIN_ASSET_PATH) or deploy dashboard assets manually",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	html := string(content)
	injected, err := h.injectAdminEnv(html)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(rewriteAdminEntryAssetPath(injected, h.adminProxyAssetBasePath())))
}

func (h *Handler) proxyLocalDev(c *gin.Context) {
	urls, err := h.getURLs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	basePath := h.adminProxyBasePath()
	assetsPath := h.adminProxyAssetBasePath()

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Admin Dev Proxy</title>
  <style>
    body { margin: 0; padding: 24px; font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color: #111; background: #fff; }
    main { max-width: 760px; margin: 0 auto; }
    code { background: #f4f4f4; border-radius: 4px; padding: 2px 6px; }
    pre { background: #f8f8f8; border: 1px solid #eee; border-radius: 8px; padding: 12px; overflow: auto; }
  </style>
</head>
<body>
  <main>
    <h1>Admin Dev Proxy</h1>
    <p>Run the local dashboard dev server and open <code>http://localhost:2333</code>.</p>
    <p>The backend route is available at <code>%s</code>.</p>
    <p>Static assets are proxied via <code>%s/*</code>.</p>
    <pre>{
  "web_url": %q,
  "gateway_url": %q,
  "base_api": %q
}</pre>
  </main>
</body>
</html>`, basePath, assetsPath, urls.WebURL, urls.WSURL, urls.BaseAPI)))
}

func (h *Handler) proxyAssetRoute(c *gin.Context) {
	canAccess, err := h.checkCanAccessAdminProxy()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if !canAccess {
		c.JSON(http.StatusForbidden, gin.H{
			"message": "admin proxy not enabled, proxy assets is forbidden",
		})
		return
	}

	relative := strings.TrimPrefix(c.Param("filepath"), "/")
	if relative == "" {
		c.Status(http.StatusNotFound)
		return
	}
	h.serveAssetRelative(c, relative)
}

func (h *Handler) proxyLegacyAssetRoute(c *gin.Context) {
	canAccess, err := h.checkCanAccessAdminProxy()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if !canAccess {
		c.JSON(http.StatusForbidden, gin.H{
			"message": "admin proxy not enabled, proxy assets is forbidden",
		})
		return
	}

	legacyRoot := strings.TrimSpace(c.Param("legacyRoot"))
	if legacyRoot == "" || legacyRoot == "qaqdmin" {
		c.Status(http.StatusNotFound)
		return
	}

	relative := strings.TrimPrefix(c.Param("filepath"), "/")
	if relative == "" {
		c.Status(http.StatusNotFound)
		return
	}
	h.serveAssetRelative(c, filepath.ToSlash(filepath.Join(legacyRoot, relative)))
}

func (h *Handler) serveAssetRelative(c *gin.Context, relative string) {
	cleanRel := strings.TrimPrefix(filepath.Clean("/"+relative), "/")
	fullPath := filepath.Join(h.adminPath, cleanRel)

	adminRoot, err := filepath.Abs(h.adminPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	targetPath, err := filepath.Abs(fullPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if targetPath != adminRoot && !strings.HasPrefix(targetPath, adminRoot+string(os.PathSeparator)) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid path"})
		return
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.Status(http.StatusNotFound)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"message": "can't serve directory"})
		return
	}

	c.Header("Cache-Control", "public, max-age=31536000")
	c.File(targetPath)
}

type injectedURLs struct {
	WebURL  string
	WSURL   string
	BaseAPI string
}

func (h *Handler) getURLs() (*injectedURLs, error) {
	cfg, err := h.cfgSvc.Get()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return &injectedURLs{
			WebURL:  "",
			WSURL:   "",
			BaseAPI: h.defaultBaseAPI(),
		}, nil
	}
	return &injectedURLs{
		WebURL:  cfg.URL.WebURL,
		WSURL:   cfg.URL.WSURL,
		BaseAPI: h.defaultBaseAPI(),
	}, nil
}

func (h *Handler) injectAdminEnv(entry string) (string, error) {
	cfg, err := h.cfgSvc.Get()
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return entry, nil
	}

	script := fmt.Sprintf(
		`<script>window.pageSource='server';window.injectData={WEB_URL:%q,LOGIN_BG:%q,BASE_API:%q,GATEWAY:%q,INIT:null};</script>`,
		cfg.URL.WebURL,
		cfg.AdminExtra.Background,
		h.defaultBaseAPI(),
		cfg.URL.WSURL,
	)

	if strings.Contains(entry, "<!-- injectable script -->") {
		return strings.Replace(entry, "<!-- injectable script -->", script, 1), nil
	}
	if strings.Contains(entry, "</head>") {
		return strings.Replace(entry, "</head>", script+"</head>", 1), nil
	}
	return script + entry, nil
}

func rewriteAdminEntryAssetPath(entry, assetBasePath string) string {
	assetBase := strings.TrimRight(strings.TrimSpace(assetBasePath), "/")
	if assetBase == "" {
		assetBase = "/proxy/qaqdmin/assets"
	}

	const proxyToken = "__MX_PROXY__"

	entry = strings.ReplaceAll(entry, `src="/proxy/`, `src="`+proxyToken+`/`)
	entry = strings.ReplaceAll(entry, `href="/proxy/`, `href="`+proxyToken+`/`)
	entry = strings.ReplaceAll(entry, `src='/proxy/`, `src='`+proxyToken+`/`)
	entry = strings.ReplaceAll(entry, `href='/proxy/`, `href='`+proxyToken+`/`)

	entry = strings.ReplaceAll(entry, `src="/`, `src="`+assetBase+`/`)
	entry = strings.ReplaceAll(entry, `href="/`, `href="`+assetBase+`/`)
	entry = strings.ReplaceAll(entry, `src='/`, `src='`+assetBase+`/`)
	entry = strings.ReplaceAll(entry, `href='/`, `href='`+assetBase+`/`)

	entry = strings.ReplaceAll(entry, `src="`+proxyToken+`/`, `src="`+assetBase+`/`)
	entry = strings.ReplaceAll(entry, `href="`+proxyToken+`/`, `href="`+assetBase+`/`)
	entry = strings.ReplaceAll(entry, `src='`+proxyToken+`/`, `src='`+assetBase+`/`)
	entry = strings.ReplaceAll(entry, `href='`+proxyToken+`/`, `href='`+assetBase+`/`)

	return entry
}

func (h *Handler) defaultBaseAPI() string {
	return "/api/v2"
}

func (h *Handler) checkCanAccessAdminProxy() (bool, error) {
	cfg, err := h.cfgSvc.Get()
	if err != nil {
		return false, err
	}
	if h.runtime != nil && h.runtime.IsDev() {
		return true, nil
	}
	if cfg == nil {
		return false, nil
	}
	return cfg.AdminExtra.EnableAdminProxy, nil
}

func (h *Handler) adminProxyBasePath() string {
	return "/proxy/qaqdmin"
}

func (h *Handler) adminProxyAssetBasePath() string {
	return h.adminProxyBasePath() + "/assets"
}
