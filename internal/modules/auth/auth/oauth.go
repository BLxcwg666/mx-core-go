package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/system/core/configs"
	"github.com/mx-space/core/internal/pkg/response"
	sessionpkg "github.com/mx-space/core/internal/pkg/session"
	"gorm.io/gorm"
)

// OAuthHandler handles social OAuth2 login flows.
type OAuthHandler struct {
	db     *gorm.DB
	cfgSvc *configs.Service
}

func NewOAuthHandler(db *gorm.DB, cfgSvc *configs.Service) *OAuthHandler {
	return &OAuthHandler{db: db, cfgSvc: cfgSvc}
}

func (h *OAuthHandler) RegisterRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/auth")

	g.GET("/providers", h.listProviders)
	g.POST("/sign-in/social", h.signInSocial) // Better Auth compatibility
	g.GET("/redirect/:provider", h.redirectToProvider)
	g.GET("/callback/:provider", h.handleCallback)
	g.DELETE("/social/:provider", middleware.Auth(h.db), h.unlinkSocial)
}

type signInSocialDTO struct {
	Provider         string `json:"provider" binding:"required"`
	CallbackURL      string `json:"callbackURL"`
	DisableRedirect  bool   `json:"disableRedirect"`
	ErrorCallbackURL string `json:"errorCallbackURL"`
}

// GET /auth/providers
func (h *OAuthHandler) listProviders(c *gin.Context) {
	cfg, err := h.cfgSvc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	providers := make([]string, 0)
	for _, p := range cfg.OAuth.Providers {
		providerType := strings.TrimSpace(p.Type)
		if p.Enabled && providerType != "" && oauthClientID(cfg.OAuth.Public, providerType) != "" {
			providers = append(providers, providerType)
		}
	}
	c.JSON(http.StatusOK, providers)
}

// GET /auth/redirect/:provider?callback_url=...
func (h *OAuthHandler) redirectToProvider(c *gin.Context) {
	providerID := c.Param("provider")
	callbackURL := c.Query("callback_url")

	provider, err := h.resolveProvider(c, providerID, callbackURL)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if provider == nil {
		response.NotFoundMsg(c, "OAuth 提供商未找到或未配置")
		return
	}

	c.Redirect(http.StatusTemporaryRedirect, provider.AuthURL)
}

// POST /auth/sign-in/social
func (h *OAuthHandler) signInSocial(c *gin.Context) {
	var dto signInSocialDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	provider, err := h.resolveProvider(c, dto.Provider, dto.CallbackURL)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if provider == nil {
		response.NotFoundMsg(c, "OAuth 提供商未找到或未配置")
		return
	}

	response.OK(c, gin.H{
		"url":      provider.AuthURL,
		"redirect": !dto.DisableRedirect,
	})
}

// GET /auth/callback/:provider?code=...&state=...
func (h *OAuthHandler) handleCallback(c *gin.Context) {
	providerID := c.Param("provider")
	code := c.Query("code")
	if code == "" {
		response.BadRequest(c, "missing code")
		return
	}

	cfg, err := h.cfgSvc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}

	var clientID, clientSecret string
	for _, p := range cfg.OAuth.Providers {
		providerType := strings.TrimSpace(p.Type)
		if strings.EqualFold(providerType, providerID) && p.Enabled {
			clientID = oauthClientID(cfg.OAuth.Public, providerType)
			clientSecret = oauthClientSecret(cfg.OAuth.Secrets, providerType)
			break
		}
	}
	if clientID == "" {
		response.NotFoundMsg(c, "OAuth 提供商未配置")
		return
	}

	accessToken, err := exchangeCode(providerID, code, clientID, clientSecret, callbackURI(c, providerID))
	if err != nil {
		response.InternalError(c, fmt.Errorf("token exchange failed: %w", err))
		return
	}

	socialUser, err := fetchSocialUser(providerID, accessToken)
	if err != nil {
		response.InternalError(c, fmt.Errorf("failed to fetch user info: %w", err))
		return
	}

	var owner models.UserModel
	if err := h.db.First(&owner).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.UnprocessableEntity(c, "no owner registered yet")
			return
		}
		response.InternalError(c, err)
		return
	}

	var existing models.OAuth2Token
	h.db.Where("user_id = ? AND provider = ?", owner.ID, providerID).First(&existing)

	now := time.Now()
	oauthRecord := models.OAuth2Token{
		UserID:      owner.ID,
		Provider:    providerID,
		ProviderUID: socialUser.ID,
		AccessToken: accessToken,
		LastUsed:    &now,
	}
	if existing.ID != "" {
		h.db.Model(&existing).Updates(map[string]interface{}{
			"provider_uid": socialUser.ID,
			"access_token": accessToken,
			"last_used":    now,
		})
	} else {
		h.db.Create(&oauthRecord)
	}

	token, _, err := sessionpkg.Issue(h.db, owner.ID, c.ClientIP(), c.Request.UserAgent(), sessionpkg.DefaultTTL)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	setAuthTokenCookie(c, token)

	if callbackURL := strings.TrimSpace(c.Query("state")); callbackURL != "" {
		if redirectWithToken(c, callbackURL, token) {
			return
		}
	}

	response.OK(c, gin.H{
		"token": token,
		"user": gin.H{
			"id":   owner.ID,
			"name": owner.Name,
		},
	})
}

// DELETE /auth/social/:provider [auth]
func (h *OAuthHandler) unlinkSocial(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	providerID := c.Param("provider")
	h.db.Where("user_id = ? AND provider = ?", userID, providerID).Delete(&models.OAuth2Token{})
	response.NoContent(c)
}

type oauthProviderDef struct {
	AuthURL string
}

type socialUserInfo struct {
	ID    string
	Login string
	Email string
	Name  string
}

func callbackURI(c *gin.Context, provider string) string {
	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
	}
	basePath := "/auth"
	fullPath := c.FullPath()
	if idx := strings.Index(fullPath, "/auth/"); idx >= 0 {
		basePath = fullPath[:idx] + "/auth"
	}
	return fmt.Sprintf("%s://%s%s/callback/%s", scheme, c.Request.Host, basePath, provider)
}

func oauthDef(providerID, clientID, callbackURL string, c *gin.Context) *oauthProviderDef {
	redirectURI := callbackURI(c, providerID)
	switch providerID {
	case "github":
		params := url.Values{}
		params.Set("client_id", clientID)
		params.Set("redirect_uri", redirectURI)
		params.Set("scope", "user:email")
		if callbackURL != "" {
			params.Set("state", callbackURL)
		}
		return &oauthProviderDef{
			AuthURL: "https://github.com/login/oauth/authorize?" + params.Encode(),
		}
	case "google":
		params := url.Values{}
		params.Set("client_id", clientID)
		params.Set("redirect_uri", redirectURI)
		params.Set("response_type", "code")
		params.Set("scope", "openid email profile")
		params.Set("access_type", "offline")
		if callbackURL != "" {
			params.Set("state", callbackURL)
		}
		return &oauthProviderDef{
			AuthURL: "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode(),
		}
	}
	return nil
}

func (h *OAuthHandler) resolveProvider(c *gin.Context, providerID, callbackURL string) (*oauthProviderDef, error) {
	cfg, err := h.cfgSvc.Get()
	if err != nil {
		return nil, err
	}
	for _, p := range cfg.OAuth.Providers {
		providerType := strings.TrimSpace(p.Type)
		clientID := oauthClientID(cfg.OAuth.Public, providerType)
		if strings.EqualFold(providerType, providerID) && p.Enabled && clientID != "" {
			return oauthDef(providerType, clientID, callbackURL, c), nil
		}
	}
	return nil, nil
}

func redirectWithToken(c *gin.Context, callbackURL, token string) bool {
	target, err := url.Parse(strings.TrimSpace(callbackURL))
	if err != nil || target == nil {
		return false
	}
	q := target.Query()
	q.Set("token", token)
	target.RawQuery = q.Encode()
	c.Redirect(http.StatusTemporaryRedirect, target.String())
	return true
}

func setAuthTokenCookie(c *gin.Context, token string) {
	const maxAge = 14 * 24 * 60 * 60
	secure := c.Request.TLS != nil
	c.SetCookie("mx-token", token, maxAge, "/", "", secure, false)
}

func clearAuthTokenCookie(c *gin.Context) {
	secure := c.Request.TLS != nil
	c.SetCookie("mx-token", "", -1, "/", "", secure, false)
}

func exchangeCode(providerID, code, clientID, clientSecret, redirectURI string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	switch providerID {
	case "github":
		body := url.Values{}
		body.Set("client_id", clientID)
		body.Set("client_secret", clientSecret)
		body.Set("code", code)
		body.Set("redirect_uri", redirectURI)

		req, _ := http.NewRequest("POST", "https://github.com/login/oauth/access_token", bytes.NewBufferString(body.Encode()))
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		var result struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", err
		}
		if result.Error != "" {
			return "", fmt.Errorf("github: %s", result.Error)
		}
		return result.AccessToken, nil

	case "google":
		body := url.Values{}
		body.Set("code", code)
		body.Set("client_id", clientID)
		body.Set("client_secret", clientSecret)
		body.Set("redirect_uri", redirectURI)
		body.Set("grant_type", "authorization_code")

		req, _ := http.NewRequest("POST", "https://oauth2.googleapis.com/token", bytes.NewBufferString(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		var result struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", err
		}
		if result.Error != "" {
			return "", fmt.Errorf("google: %s", result.Error)
		}
		return result.AccessToken, nil
	}

	return "", fmt.Errorf("unsupported provider: %s", providerID)
}

func fetchSocialUser(providerID, accessToken string) (*socialUserInfo, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	switch providerID {
	case "github":
		req, _ := http.NewRequest("GET", "https://api.github.com/user", nil)
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		var u struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Email string `json:"email"`
			Name  string `json:"name"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
			return nil, err
		}
		return &socialUserInfo{
			ID:    fmt.Sprintf("%d", u.ID),
			Login: u.Login,
			Email: u.Email,
			Name:  u.Name,
		}, nil

	case "google":
		req, _ := http.NewRequest("GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		var u struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Name  string `json:"name"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
			return nil, err
		}
		return &socialUserInfo{
			ID:    u.ID,
			Email: u.Email,
			Name:  u.Name,
		}, nil
	}

	return nil, fmt.Errorf("unsupported provider: %s", providerID)
}

func oauthClientID(public map[string]interface{}, providerType string) string {
	return oauthClientField(public, providerType, "client_id", "clientId")
}

func oauthClientSecret(secrets map[string]interface{}, providerType string) string {
	return oauthClientField(secrets, providerType, "client_secret", "clientSecret")
}

func oauthClientField(source map[string]interface{}, providerType string, keys ...string) string {
	if len(source) == 0 || strings.TrimSpace(providerType) == "" {
		return ""
	}
	raw, ok := source[providerType]
	if !ok {
		for k, v := range source {
			if strings.EqualFold(k, providerType) {
				raw = v
				ok = true
				break
			}
		}
		if !ok {
			return ""
		}
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return ""
	}
	for _, key := range keys {
		if value, ok := m[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
