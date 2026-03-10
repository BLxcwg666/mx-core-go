package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	appcfg "github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/system/core/configs"
	"github.com/mx-space/core/internal/pkg/response"
	sessionpkg "github.com/mx-space/core/internal/pkg/session"
	"gorm.io/gorm"
)

const oauthStateTTL = 10 * time.Minute

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
		if p.Enabled && providerType != "" && oauthClientID(cfg.OAuth.Public, providerType) != "" && oauthClientSecret(cfg.OAuth.Secrets, providerType) != "" {
			providers = append(providers, providerType)
		}
	}
	response.OK(c, providers)
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

	state, err := parseOAuthState(c.Query("state"), providerID, clientSecret)
	if err != nil {
		response.BadRequest(c, "invalid oauth state")
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
	if strings.TrimSpace(socialUser.ID) == "" {
		response.ForbiddenMsg(c, "OAuth 账号信息无效")
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

	currentUserID := h.currentAuthenticatedUserID(c)
	allowLink := currentUserID != "" && currentUserID == owner.ID
	linkedByRecord := existing.ID != "" && strings.TrimSpace(existing.ProviderUID) != "" && strings.TrimSpace(existing.ProviderUID) == strings.TrimSpace(socialUser.ID)
	linkedByProfile := ownerHasLinkedSocialID(owner.SocialIDs, providerID, socialUser.ID)
	if !allowLink && !linkedByRecord && !linkedByProfile {
		response.ForbiddenMsg(c, "社交账号未绑定到当前站点主人")
		return
	}

	now := time.Now()
	if existing.ID != "" {
		updates := map[string]interface{}{
			"access_token": accessToken,
			"last_used":    now,
		}
		if allowLink || linkedByRecord || linkedByProfile {
			updates["provider_uid"] = socialUser.ID
		}
		if err := h.db.Model(&existing).Updates(updates).Error; err != nil {
			response.InternalError(c, err)
			return
		}
	} else {
		oauthRecord := models.OAuth2Token{
			UserID:      owner.ID,
			Provider:    providerID,
			ProviderUID: socialUser.ID,
			AccessToken: accessToken,
			LastUsed:    &now,
		}
		if err := h.db.Create(&oauthRecord).Error; err != nil {
			response.InternalError(c, err)
			return
		}
	}

	token, _, err := sessionpkg.Issue(h.db, owner.ID, c.ClientIP(), c.Request.UserAgent(), sessionpkg.DefaultTTL)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	setAuthTokenCookie(c, token)

	if state.CallbackURL != "" {
		if redirectWithToken(c, state.CallbackURL, token) {
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

func oauthDef(providerID, clientID, stateToken string, c *gin.Context) *oauthProviderDef {
	redirectURI := callbackURI(c, providerID)
	switch providerID {
	case "github":
		params := url.Values{}
		params.Set("client_id", clientID)
		params.Set("redirect_uri", redirectURI)
		params.Set("scope", "user:email")
		if stateToken != "" {
			params.Set("state", stateToken)
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
		if stateToken != "" {
			params.Set("state", stateToken)
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
	validatedCallbackURL, err := validateOAuthCallbackURL(callbackURL, c, cfg)
	if err != nil {
		return nil, err
	}
	for _, p := range cfg.OAuth.Providers {
		providerType := strings.TrimSpace(p.Type)
		clientID := oauthClientID(cfg.OAuth.Public, providerType)
		clientSecret := oauthClientSecret(cfg.OAuth.Secrets, providerType)
		if strings.EqualFold(providerType, providerID) && p.Enabled && clientID != "" && clientSecret != "" {
			stateToken, err := buildOAuthState(providerType, validatedCallbackURL, clientSecret)
			if err != nil {
				return nil, err
			}
			return oauthDef(providerType, clientID, stateToken, c), nil
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

type oauthStatePayload struct {
	Provider    string `json:"provider"`
	CallbackURL string `json:"callback_url,omitempty"`
	IssuedAt    int64  `json:"issued_at"`
	Nonce       string `json:"nonce"`
}

func buildOAuthState(providerID, callbackURL, signingKey string) (string, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if providerID == "" || strings.TrimSpace(signingKey) == "" {
		return "", fmt.Errorf("oauth state signing secret is unavailable")
	}
	nonce, err := randomOAuthStateNonce()
	if err != nil {
		return "", err
	}
	payloadBytes, err := json.Marshal(oauthStatePayload{
		Provider:    providerID,
		CallbackURL: strings.TrimSpace(callbackURL),
		IssuedAt:    time.Now().Unix(),
		Nonce:       nonce,
	})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(signingKey))
	_, _ = mac.Write(payloadBytes)
	return base64.RawURLEncoding.EncodeToString(payloadBytes) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func parseOAuthState(rawState, providerID, signingKey string) (*oauthStatePayload, error) {
	stateToken := strings.TrimSpace(rawState)
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if stateToken == "" || providerID == "" || strings.TrimSpace(signingKey) == "" {
		return nil, fmt.Errorf("oauth state is invalid")
	}
	parts := strings.Split(stateToken, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("oauth state is invalid")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	providedMAC, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(signingKey))
	_, _ = mac.Write(payloadBytes)
	if !hmac.Equal(providedMAC, mac.Sum(nil)) {
		return nil, fmt.Errorf("oauth state signature mismatch")
	}
	var payload oauthStatePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, err
	}
	if strings.ToLower(strings.TrimSpace(payload.Provider)) != providerID {
		return nil, fmt.Errorf("oauth state provider mismatch")
	}
	issuedAt := time.Unix(payload.IssuedAt, 0)
	if payload.IssuedAt == 0 || time.Since(issuedAt) > oauthStateTTL || issuedAt.After(time.Now().Add(30*time.Second)) {
		return nil, fmt.Errorf("oauth state expired")
	}
	return &payload, nil
}

func randomOAuthStateNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func validateOAuthCallbackURL(raw string, c *gin.Context, cfg *appcfg.FullConfig) (string, error) {
	callbackURL := strings.TrimSpace(raw)
	if callbackURL == "" {
		return "", nil
	}
	target, err := url.Parse(callbackURL)
	if err != nil || target == nil {
		return "", fmt.Errorf("invalid callback url")
	}
	if target.Scheme == "" && target.Host == "" {
		if !strings.HasPrefix(target.Path, "/") {
			return "", fmt.Errorf("invalid callback url")
		}
		return target.String(), nil
	}
	if !strings.EqualFold(target.Scheme, "http") && !strings.EqualFold(target.Scheme, "https") {
		return "", fmt.Errorf("invalid callback url scheme")
	}
	allowedOrigins := oauthAllowedCallbackOrigins(c, cfg)
	if _, ok := allowedOrigins[normalizeOAuthOrigin(target)]; !ok {
		return "", fmt.Errorf("callback url origin not allowed")
	}
	return target.String(), nil
}

func oauthAllowedCallbackOrigins(c *gin.Context, cfg *appcfg.FullConfig) map[string]struct{} {
	allowed := map[string]struct{}{}
	addOAuthOrigin(allowed, c.GetHeader("Origin"))
	addOAuthOrigin(allowed, requestOrigin(c))
	if cfg != nil {
		addOAuthOrigin(allowed, cfg.URL.AdminURL)
		addOAuthOrigin(allowed, cfg.URL.WebURL)
		addOAuthOrigin(allowed, cfg.URL.ServerURL)
	}
	return allowed
}

func addOAuthOrigin(dest map[string]struct{}, raw string) {
	origin := normalizeOAuthOriginString(raw)
	if origin == "" {
		return
	}
	dest[origin] = struct{}{}
}

func normalizeOAuthOriginString(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	target, err := url.Parse(text)
	if err != nil || target == nil {
		return ""
	}
	return normalizeOAuthOrigin(target)
}

func normalizeOAuthOrigin(target *url.URL) string {
	if target == nil || target.Scheme == "" || target.Host == "" {
		return ""
	}
	return strings.ToLower(target.Scheme) + "://" + strings.ToLower(target.Host)
}

func requestOrigin(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
	}
	if strings.TrimSpace(c.Request.Host) == "" {
		return ""
	}
	return scheme + "://" + c.Request.Host
}

func (h *OAuthHandler) currentAuthenticatedUserID(c *gin.Context) string {
	rawToken := extractAuthTokenFromRequest(c)
	if rawToken == "" {
		return ""
	}
	claims, err := middleware.ValidateTokenClaims(h.db, rawToken)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(claims.UserID)
}

func ownerHasLinkedSocialID(raw, providerID, socialUserID string) bool {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(providerID) == "" || strings.TrimSpace(socialUserID) == "" {
		return false
	}
	ids := map[string]interface{}{}
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return false
	}
	for key, value := range ids {
		if strings.EqualFold(strings.TrimSpace(key), providerID) && strings.TrimSpace(fmt.Sprint(value)) == strings.TrimSpace(socialUserID) {
			return true
		}
	}
	return false
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
