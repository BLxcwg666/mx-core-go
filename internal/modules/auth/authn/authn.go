package authn

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
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

	var user models.UserModel
	if err := h.db.Select("id, username, name").First(&user, "id = ?", userID).Error; err != nil {
		response.NotFoundMsg(c, "用户不存在")
		return
	}

	challenge := randomBase64URL(32)
	authnChallenges.set("registration:"+userID, challenge)

	rpID := deriveRPID(c, h.db)

	var authnItems []models.AuthnModel
	_ = h.db.Order("created_at DESC").Find(&authnItems).Error

	excludeCredentials := make([]gin.H, 0, len(authnItems))
	for _, item := range authnItems {
		excludeCredentials = append(excludeCredentials, gin.H{
			"id":   base64.RawURLEncoding.EncodeToString(item.CredentialID),
			"type": "public-key",
		})
	}

	displayName := user.Name
	if strings.TrimSpace(displayName) == "" {
		displayName = user.Username
	}

	response.OK(c, gin.H{
		"challenge": challenge,
		"rp": gin.H{
			"name": "MixSpace",
			"id":   rpID,
		},
		"user": gin.H{
			"id":          base64.RawURLEncoding.EncodeToString([]byte(user.ID)),
			"name":        user.Username,
			"displayName": displayName,
		},
		"pubKeyCredParams": []gin.H{
			{"type": "public-key", "alg": -7},
			{"type": "public-key", "alg": -257},
		},
		"timeout":            60000,
		"attestation":        "none",
		"excludeCredentials": excludeCredentials,
		"authenticatorSelection": gin.H{
			"residentKey":             "preferred",
			"userVerification":        "preferred",
			"authenticatorAttachment": "platform",
		},
	})
}

func (h *Handler) registerVerify(c *gin.Context) {
	userID := middleware.CurrentUserID(c)
	if userID == "" {
		response.Unauthorized(c)
		return
	}

	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	challenge, err := extractClientDataChallenge(body)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	expectedChallenge := authnChallenges.get("registration:" + userID)
	if expectedChallenge == "" || challenge != expectedChallenge {
		response.BadRequest(c, "Challenge 不存在")
		return
	}

	id := strAny(body["id"])
	if id == "" {
		response.BadRequest(c, "credential id missing")
		return
	}
	credentialID, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		response.BadRequest(c, "invalid credential id")
		return
	}

	name := strings.TrimSpace(strAny(body["name"]))
	if name == "" {
		name = "Passkey"
	}
	name = h.ensureUniqueName(name)

	var credentialPublicKey []byte
	if respMap, ok := body["response"].(map[string]interface{}); ok {
		if attestation, ok := respMap["attestationObject"].(string); ok && attestation != "" {
			if decoded, err := base64.RawURLEncoding.DecodeString(attestation); err == nil {
				credentialPublicKey = decoded
			}
		}
	}

	authnItem := models.AuthnModel{
		Name:                 name,
		CredentialID:         credentialID,
		CredentialPublicKey:  credentialPublicKey,
		Counter:              0,
		CredentialDeviceType: "singleDevice",
		CredentialBackedUp:   false,
	}
	if err := h.db.Create(&authnItem).Error; err != nil {
		response.InternalError(c, err)
		return
	}

	authnChallenges.del("registration:" + userID)
	response.OK(c, gin.H{"verified": true})
}

func (h *Handler) authenticationOptions(c *gin.Context) {
	challenge := randomBase64URL(32)
	authnChallenges.set("authentication", challenge)

	var authnItems []models.AuthnModel
	_ = h.db.Order("created_at DESC").Find(&authnItems).Error

	allowCredentials := make([]gin.H, 0, len(authnItems))
	for _, item := range authnItems {
		allowCredentials = append(allowCredentials, gin.H{
			"id":   base64.RawURLEncoding.EncodeToString(item.CredentialID),
			"type": "public-key",
		})
	}

	rpID := deriveRPID(c, h.db)
	response.OK(c, gin.H{
		"challenge":        challenge,
		"rpId":             rpID,
		"timeout":          60000,
		"allowCredentials": allowCredentials,
		"userVerification": "preferred",
	})
}

func (h *Handler) authenticationVerify(c *gin.Context) {
	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	challenge, err := extractClientDataChallenge(body)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	expectedChallenge := authnChallenges.get("authentication")
	if expectedChallenge == "" || challenge != expectedChallenge {
		response.BadRequest(c, "Challenge 不存在")
		return
	}

	credentialIDB64 := strAny(body["id"])
	if credentialIDB64 == "" {
		response.BadRequest(c, "credential id missing")
		return
	}
	credentialID, err := base64.RawURLEncoding.DecodeString(credentialIDB64)
	if err != nil {
		response.BadRequest(c, "invalid credential id")
		return
	}

	var count int64
	if err := h.db.Model(&models.AuthnModel{}).
		Where("credential_id = ?", credentialID).
		Count(&count).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	if count == 0 {
		response.BadRequest(c, "认证失败")
		return
	}

	testMode, _ := body["test"].(bool)
	res := gin.H{"verified": true}

	if !testMode {
		var owner models.UserModel
		if err := h.db.Select("id").First(&owner).Error; err != nil {
			response.InternalError(c, err)
			return
		}
		token, _, err := sessionpkg.Issue(h.db, owner.ID, c.ClientIP(), c.Request.UserAgent(), sessionpkg.DefaultTTL)
		if err != nil {
			response.InternalError(c, err)
			return
		}
		res["token"] = token
	}

	authnChallenges.del("authentication")
	response.OK(c, res)
}

func (h *Handler) listItems(c *gin.Context) {
	var items []models.AuthnModel
	if err := h.db.Order("created_at DESC").Find(&items).Error; err != nil {
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
	if err := h.db.Delete(&models.AuthnModel{}, "id = ?", c.Param("id")).Error; err != nil {
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

type clientDataPayload struct {
	Challenge string `json:"challenge"`
}

func extractClientDataChallenge(body map[string]interface{}) (string, error) {
	respMap, ok := body["response"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("response missing")
	}
	clientDataB64, ok := respMap["clientDataJSON"].(string)
	if !ok || clientDataB64 == "" {
		return "", fmt.Errorf("clientDataJSON missing")
	}
	clientDataBytes, err := base64.RawURLEncoding.DecodeString(clientDataB64)
	if err != nil {
		return "", fmt.Errorf("invalid clientDataJSON")
	}
	var payload clientDataPayload
	if err := json.Unmarshal(clientDataBytes, &payload); err != nil {
		return "", fmt.Errorf("invalid clientDataJSON payload")
	}
	if payload.Challenge == "" {
		return "", fmt.Errorf("challenge missing")
	}
	return payload.Challenge, nil
}

func randomBase64URL(size int) string {
	buf := make([]byte, size)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

type challengeStore struct {
	mu sync.RWMutex
	m  map[string]string
}

func (s *challengeStore) set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[key] = value
}

func (s *challengeStore) get(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.m[key]
}

func (s *challengeStore) del(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
}

var authnChallenges = &challengeStore{m: map[string]string{}}
