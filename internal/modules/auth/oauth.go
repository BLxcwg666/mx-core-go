package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/configs"
	jwtpkg "github.com/mx-space/core/internal/pkg/jwt"
	"github.com/mx-space/core/internal/pkg/response"
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
	g.GET("/redirect/:provider", h.redirectToProvider)
	g.GET("/callback/:provider", h.handleCallback)
	g.DELETE("/social/:provider", middleware.Auth(h.db), h.unlinkSocial)
}

type providerInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// GET /auth/providers
func (h *OAuthHandler) listProviders(c *gin.Context) {
	cfg, err := h.cfgSvc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	var providers []providerInfo
	for _, p := range cfg.OAuth.Providers {
		if p.Enabled && p.ClientID != "" {
			providers = append(providers, providerInfo{
				ID:      p.ID,
				Name:    p.Name,
				Enabled: true,
			})
		}
	}
	if providers == nil {
		providers = []providerInfo{}
	}
	response.OK(c, providers)
}

// GET /auth/redirect/:provider?callback_url=...
func (h *OAuthHandler) redirectToProvider(c *gin.Context) {
	providerID := c.Param("provider")
	callbackURL := c.Query("callback_url")

	cfg, err := h.cfgSvc.Get()
	if err != nil {
		response.InternalError(c, err)
		return
	}

	var provider *oauthProviderDef
	for _, p := range cfg.OAuth.Providers {
		if p.ID == providerID && p.Enabled && p.ClientID != "" {
			def := oauthDef(providerID, p.ClientID, callbackURL, c)
			if def != nil {
				provider = def
				break
			}
		}
	}

	if provider == nil {
		response.NotFoundMsg(c, "OAuth provider not found or not configured")
		return
	}

	c.Redirect(http.StatusTemporaryRedirect, provider.AuthURL)
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
		if p.ID == providerID && p.Enabled {
			clientID = p.ClientID
			clientSecret = p.ClientSecret
			break
		}
	}
	if clientID == "" {
		response.NotFoundMsg(c, "provider not configured")
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

	token, err := jwtpkg.Sign(owner.ID, 30*24*time.Hour)
	if err != nil {
		response.InternalError(c, err)
		return
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
	return fmt.Sprintf("%s://%s/auth/callback/%s", scheme, c.Request.Host, provider)
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
