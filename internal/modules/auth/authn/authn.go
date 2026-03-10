package authn

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	gowauthn "github.com/go-webauthn/webauthn/webauthn"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/response"
	sessionpkg "github.com/mx-space/core/internal/pkg/session"
	"gorm.io/gorm"
)

type Handler struct {
	db *gorm.DB
}

func NewHandler(db *gorm.DB) *Handler { return &Handler{db: db} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/passkey")
	g.POST("/register", authMW, h.registerOptions)
	g.POST("/register/verify", authMW, h.registerVerify)
	g.POST("/authentication", h.authenticationOptions)
	g.POST("/authentication/verify", h.authenticationVerify)
	g.GET("/items", authMW, h.listItems)
	g.DELETE("/items/:id", authMW, h.deleteItem)
}

func (h *Handler) registerOptions(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	if userID == "" {
		response.Unauthorized(c)
		return
	}

	user, err := h.loadWebAuthnUser(userID, false)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if user == nil {
		response.NotFoundMsg(c, "用户不存在")
		return
	}

	wa, err := h.newWebAuthn(c)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	creation, sessionData, err := wa.BeginRegistration(
		user,
		gowauthn.WithExclusions(gowauthn.Credentials(user.WebAuthnCredentials()).CredentialDescriptors()),
	)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	authnSessions.set("registration:"+userID, sessionData)
	response.OK(c, creation.Response)
}

func (h *Handler) registerVerify(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	if userID == "" {
		response.Unauthorized(c)
		return
	}

	body, err := readRequestBodyJSON(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	user, err := h.loadWebAuthnUser(userID, false)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if user == nil {
		response.NotFoundMsg(c, "用户不存在")
		return
	}

	sessionData, ok := authnSessions.get("registration:" + userID)
	if !ok {
		response.BadRequest(c, "Challenge 不存在")
		return
	}

	wa, err := h.newWebAuthn(c)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	credential, err := wa.FinishRegistration(user, *sessionData, c.Request)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	name := strings.TrimSpace(strAny(body["name"]))
	if name == "" {
		name = "Passkey"
	}
	name = h.ensureUniqueName(name)

	authnItem := models.AuthnModel{
		UserID:               userID,
		Name:                 name,
		CredentialID:         credential.ID,
		CredentialPublicKey:  credential.PublicKey,
		CredentialJSON:       string(credentialJSON),
		Counter:              credential.Authenticator.SignCount,
		CredentialDeviceType: string(credential.Authenticator.Attachment),
		CredentialBackedUp:   credential.Flags.BackupState,
	}
	if err := h.db.Create(&authnItem).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	authnSessions.del("registration:" + userID)
	response.OK(c, gin.H{"verified": true})
}

func (h *Handler) authenticationOptions(c *gin.Context) {
	user, err := h.loadOwnerWebAuthnUser(false)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if user == nil || len(user.WebAuthnCredentials()) == 0 {
		response.BadRequest(c, "暂无可用 Passkey")
		return
	}

	wa, err := h.newWebAuthn(c)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	assertion, sessionData, err := wa.BeginLogin(user)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	authnSessions.set("authentication:"+user.user.ID, sessionData)
	response.OK(c, assertion.Response)
}

func (h *Handler) authenticationVerify(c *gin.Context) {
	body, err := readRequestBodyJSON(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	user, err := h.loadOwnerWebAuthnUser(false)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if user == nil || len(user.WebAuthnCredentials()) == 0 {
		response.BadRequest(c, "暂无可用 Passkey")
		return
	}

	sessionData, ok := authnSessions.get("authentication:" + user.user.ID)
	if !ok {
		response.BadRequest(c, "Challenge 不存在")
		return
	}

	wa, err := h.newWebAuthn(c)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	credential, err := wa.FinishLogin(user, *sessionData, c.Request)
	if err != nil {
		response.BadRequest(c, "认证失败")
		return
	}
	if err := h.updateStoredCredential(user.user.ID, credential); err != nil {
		response.InternalError(c, err)
		return
	}

	testMode, _ := body["test"].(bool)
	res := gin.H{"verified": true}
	if !testMode {
		token, _, err := sessionpkg.Issue(h.db, user.user.ID, c.ClientIP(), c.Request.UserAgent(), sessionpkg.DefaultTTL)
		if err != nil {
			response.InternalError(c, err)
			return
		}
		res["token"] = token
	}

	authnSessions.del("authentication:" + user.user.ID)
	response.OK(c, res)
}

func (h *Handler) listItems(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	items, err := h.loadAuthnItems(userID, true)
	if err != nil {
		response.InternalError(c, err)
		return
	}

	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		out = append(out, gin.H{
			"id":                   item.ID,
			"name":                 item.Name,
			"credentialID":         base64.RawURLEncoding.EncodeToString(item.CredentialID),
			"credentialPublicKey":  base64.RawURLEncoding.EncodeToString(item.CredentialPublicKey),
			"counter":              item.Counter,
			"credentialDeviceType": item.CredentialDeviceType,
			"credentialBackedUp":   item.CredentialBackedUp,
			"created":              item.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) deleteItem(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	if err := h.db.
		Where("id = ? AND (user_id = ? OR user_id = '' OR user_id IS NULL)", c.Param("id"), userID).
		Delete(&models.AuthnModel{}).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) ensureUniqueName(base string) string {
	name := base
	for i := 1; i < 1000; i++ {
		var count int64
		_ = h.db.Model(&models.AuthnModel{}).Where("name = ?", name).Count(&count).Error
		if count == 0 {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixMilli())
}

func (h *Handler) newWebAuthn(c *gin.Context) (*gowauthn.WebAuthn, error) {
	return gowauthn.New(&gowauthn.Config{
		RPDisplayName:         "MixSpace",
		RPID:                  deriveRPID(c, h.db),
		RPOrigins:             deriveRPOrigins(c, h.db),
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			AuthenticatorAttachment: protocol.Platform,
			RequireResidentKey:      protocol.ResidentKeyNotRequired(),
			ResidentKey:             protocol.ResidentKeyRequirementPreferred,
			UserVerification:        protocol.VerificationPreferred,
		},
		Timeouts: gowauthn.TimeoutsConfig{
			Login: gowauthn.TimeoutConfig{
				Enforce:    true,
				Timeout:    time.Minute,
				TimeoutUVD: time.Minute,
			},
			Registration: gowauthn.TimeoutConfig{
				Enforce:    true,
				Timeout:    time.Minute,
				TimeoutUVD: time.Minute,
			},
		},
	})
}

func (h *Handler) loadOwnerWebAuthnUser(includeLegacy bool) (*webAuthnUser, error) {
	var owner models.UserModel
	if err := h.db.Select("id, username, name").First(&owner).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return h.buildWebAuthnUser(&owner, includeLegacy)
}

func (h *Handler) loadWebAuthnUser(userID string, includeLegacy bool) (*webAuthnUser, error) {
	var user models.UserModel
	if err := h.db.Select("id, username, name").First(&user, "id = ?", userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return h.buildWebAuthnUser(&user, includeLegacy)
}

func (h *Handler) buildWebAuthnUser(user *models.UserModel, includeLegacy bool) (*webAuthnUser, error) {
	if user == nil {
		return nil, nil
	}
	items, err := h.loadAuthnItems(user.ID, includeLegacy)
	if err != nil {
		return nil, err
	}
	credentials := make([]gowauthn.Credential, 0, len(items))
	for _, item := range items {
		credential, ok := parseStoredCredential(item.CredentialJSON)
		if !ok {
			continue
		}
		credentials = append(credentials, credential)
	}
	return &webAuthnUser{user: *user, credentials: credentials}, nil
}

func (h *Handler) loadAuthnItems(userID string, includeLegacy bool) ([]models.AuthnModel, error) {
	var items []models.AuthnModel
	query := h.db.Order("created_at DESC")
	if includeLegacy {
		query = query.Where("user_id = ? OR user_id = '' OR user_id IS NULL", userID)
	} else {
		query = query.Where("user_id = ?", userID)
	}
	if err := query.Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (h *Handler) updateStoredCredential(userID string, credential *gowauthn.Credential) error {
	if strings.TrimSpace(userID) == "" || credential == nil {
		return fmt.Errorf("invalid credential update")
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	updates := map[string]interface{}{
		"user_id":                userID,
		"credential_public_key":  credential.PublicKey,
		"credential_json":        string(credentialJSON),
		"counter":                credential.Authenticator.SignCount,
		"credential_device_type": string(credential.Authenticator.Attachment),
		"credential_backed_up":   credential.Flags.BackupState,
	}
	res := h.db.Model(&models.AuthnModel{}).
		Where("user_id = ? AND credential_id = ?", userID, credential.ID).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	res = h.db.Model(&models.AuthnModel{}).
		Where("(user_id = '' OR user_id IS NULL) AND credential_id = ?", credential.ID).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("stored credential not found")
	}
	return nil
}

type webAuthnUser struct {
	user        models.UserModel
	credentials []gowauthn.Credential
}

func (u *webAuthnUser) WebAuthnID() []byte {
	return []byte(u.user.ID)
}

func (u *webAuthnUser) WebAuthnName() string {
	if strings.TrimSpace(u.user.Username) != "" {
		return u.user.Username
	}
	return u.user.ID
}

func (u *webAuthnUser) WebAuthnDisplayName() string {
	if strings.TrimSpace(u.user.Name) != "" {
		return u.user.Name
	}
	return u.WebAuthnName()
}

func (u *webAuthnUser) WebAuthnCredentials() []gowauthn.Credential {
	out := make([]gowauthn.Credential, len(u.credentials))
	copy(out, u.credentials)
	return out
}

func readRequestBodyJSON(c *gin.Context) (map[string]interface{}, error) {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return nil, fmt.Errorf("request body missing")
	}
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(rawBody))
	if len(bytes.TrimSpace(rawBody)) == 0 {
		return nil, fmt.Errorf("request body missing")
	}
	body := map[string]interface{}{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return nil, err
	}
	return body, nil
}

func parseStoredCredential(raw string) (gowauthn.Credential, bool) {
	if strings.TrimSpace(raw) == "" {
		return gowauthn.Credential{}, false
	}
	var credential gowauthn.Credential
	if err := json.Unmarshal([]byte(raw), &credential); err != nil {
		return gowauthn.Credential{}, false
	}
	if len(credential.ID) == 0 || len(credential.PublicKey) == 0 {
		return gowauthn.Credential{}, false
	}
	return credential, true
}

func strAny(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func deriveRPID(c *gin.Context, db *gorm.DB) string {
	origin := c.GetHeader("Origin")
	if origin != "" {
		if u, err := url.Parse(origin); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}

	var opt models.OptionModel
	if err := db.Where("name = ?", "configs").First(&opt).Error; err == nil && opt.Value != "" {
		cfg := struct {
			URL struct {
				WebURL    string `json:"web_url"`
				AdminURL  string `json:"admin_url"`
				ServerURL string `json:"server_url"`
			} `json:"url"`
		}{}
		if json.Unmarshal([]byte(opt.Value), &cfg) == nil {
			adminURL := firstNonEmpty(cfg.URL.AdminURL, cfg.URL.WebURL, cfg.URL.ServerURL)
			if adminURL != "" {
				if u, err := url.Parse(adminURL); err == nil && u.Hostname() != "" {
					return u.Hostname()
				}
			}
		}
	}

	host := c.Request.Host
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	if host == "" {
		host = "localhost"
	}
	return host
}

func deriveRPOrigins(c *gin.Context, db *gorm.DB) []string {
	originSet := map[string]struct{}{}
	addOrigin := func(raw string) {
		text := strings.TrimSpace(raw)
		if text == "" {
			return
		}
		u, err := url.Parse(text)
		if err != nil || u == nil || u.Scheme == "" || u.Host == "" {
			return
		}
		if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
			return
		}
		originSet[strings.ToLower(u.Scheme)+"://"+strings.ToLower(u.Host)] = struct{}{}
	}

	addOrigin(c.GetHeader("Origin"))

	var opt models.OptionModel
	if err := db.Where("name = ?", "configs").First(&opt).Error; err == nil && opt.Value != "" {
		cfg := struct {
			URL struct {
				WebURL    string `json:"web_url"`
				AdminURL  string `json:"admin_url"`
				ServerURL string `json:"server_url"`
			} `json:"url"`
		}{}
		if json.Unmarshal([]byte(opt.Value), &cfg) == nil {
			addOrigin(cfg.URL.AdminURL)
			addOrigin(cfg.URL.WebURL)
			addOrigin(cfg.URL.ServerURL)
		}
	}

	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
	}
	addOrigin(scheme + "://" + c.Request.Host)

	origins := make([]string, 0, len(originSet))
	for origin := range originSet {
		origins = append(origins, origin)
	}
	if len(origins) == 0 {
		origins = append(origins, "http://localhost")
	}
	return origins
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

type authnSessionStore struct {
	mu sync.RWMutex
	m  map[string]*gowauthn.SessionData
}

func (s *authnSessionStore) set(key string, value *gowauthn.SessionData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]*gowauthn.SessionData{}
	}
	s.m[key] = value
}

func (s *authnSessionStore) get(key string) (*gowauthn.SessionData, bool) {
	s.mu.RLock()
	value := s.m[key]
	s.mu.RUnlock()
	if value == nil {
		return nil, false
	}
	if !value.Expires.IsZero() && value.Expires.Before(time.Now()) {
		s.del(key)
		return nil, false
	}
	return value, true
}

func (s *authnSessionStore) del(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
}

var authnSessions = &authnSessionStore{m: map[string]*gowauthn.SessionData{}}
