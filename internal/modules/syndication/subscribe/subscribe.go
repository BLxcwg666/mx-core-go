package subscribe

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/models"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	pkgmail "github.com/mx-space/core/internal/pkg/mail"
	"github.com/mx-space/core/internal/pkg/response"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SubscribeBit is a bitmask for subscription topics.
const (
	SubscribePostCreateBit     = 1 << 0 // post_c
	SubscribeNoteCreateBit     = 1 << 1 // note_c
	SubscribeSayCreateBit      = 1 << 2 // say_c
	SubscribeRecentCreateBit   = 1 << 3 // recently_c
	SubscribeAllBit            = SubscribePostCreateBit | SubscribeNoteCreateBit | SubscribeSayCreateBit | SubscribeRecentCreateBit
	SubscribeLegacyCommentBit  = SubscribeSayCreateBit // compatibility alias
	SubscribeLegacyRecentlyBit = SubscribeRecentCreateBit
)

var subscribeTypeToBitMap = map[string]int{
	"post_c":     SubscribePostCreateBit,
	"note_c":     SubscribeNoteCreateBit,
	"say_c":      SubscribeSayCreateBit,
	"recently_c": SubscribeRecentCreateBit,
	"all":        SubscribeAllBit,
}

type SubscribeDTO struct {
	Email     string   `json:"email"     binding:"required,email"`
	Subscribe *int     `json:"subscribe"` // legacy bitmask
	Types     []string `json:"types"`     // preferred format
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) Subscribe(dto *SubscribeDTO) (*models.SubscribeModel, error) {
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	cancelToken := hex.EncodeToString(token)

	sub := models.SubscribeModel{
		Email:       dto.Email,
		CancelToken: cancelToken,
		Subscribe:   normalizeSubscribe(dto),
		Verified:    false,
	}

	result := s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "email"}},
		DoUpdates: clause.AssignmentColumns([]string{"subscribe", "cancel_token", "verified"}),
	}).Create(&sub)

	return &sub, result.Error
}

func (s *Service) Verify(cancelToken string) error {
	result := s.db.Model(&models.SubscribeModel{}).
		Where("cancel_token = ?", cancelToken).
		Update("verified", true)
	if result.RowsAffected == 0 {
		return fmt.Errorf("invalid token")
	}
	return result.Error
}

func (s *Service) Unsubscribe(cancelToken string) error {
	result := s.db.Where("cancel_token = ?", cancelToken).
		Delete(&models.SubscribeModel{})
	if result.RowsAffected == 0 {
		return fmt.Errorf("not found")
	}
	return result.Error
}

func (s *Service) UnsubscribeByEmail(email string) error {
	result := s.db.Where("email = ?", email).Delete(&models.SubscribeModel{})
	if result.RowsAffected == 0 {
		return fmt.Errorf("not found")
	}
	return result.Error
}

func (s *Service) BatchUnsubscribe(emails []string, all bool) (int64, error) {
	query := s.db.Model(&models.SubscribeModel{})
	if !all {
		if len(emails) == 0 {
			return 0, nil
		}
		query = query.Where("email IN ?", emails)
	}
	result := query.Delete(&models.SubscribeModel{})
	return result.RowsAffected, result.Error
}

func (s *Service) GetByEmail(email string) (*models.SubscribeModel, error) {
	var sub models.SubscribeModel
	if err := s.db.Where("email = ?", email).First(&sub).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &sub, nil
}

// GetSubscribers returns all verified subscribers for a given topic bit.
func (s *Service) GetSubscribers(bit int) ([]models.SubscribeModel, error) {
	var subs []models.SubscribeModel
	err := s.db.Where("verified = ? AND (subscribe & ?) != 0", true, bit).Find(&subs).Error
	return subs, err
}

type Handler struct {
	svc    *Service
	cfgSvc *appconfigs.Service
}

func NewHandler(svc *Service, cfgSvc *appconfigs.Service) *Handler {
	return &Handler{svc: svc, cfgSvc: cfgSvc}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/subscribe")
	g.GET("/status", h.status)
	g.POST("", h.subscribe)
	g.GET("/verify", h.verify)      // ?token=...
	g.GET("/cancel", h.unsubscribe) // ?token=...
	g.GET("/unsubscribe", h.unsubscribe)
	g.DELETE("/unsubscribe/batch", authMW, h.unsubscribeBatch)
	g.GET("", authMW, h.list)
}

func (h *Handler) subscribe(c *gin.Context) {
	var dto SubscribeDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	enabled, err := h.isSubscribeEnabled()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if !enabled {
		response.BadRequest(c, "订阅功能未开启")
		return
	}
	mask, hasInvalidType := normalizeSubscribeWithValidation(&dto)
	if hasInvalidType {
		response.BadRequest(c, "订阅类型无效")
		return
	}
	if mask <= 0 {
		response.BadRequest(c, "订阅类型不能为空")
		return
	}
	sub, err := h.svc.Subscribe(&dto)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if err := h.sendVerifyEmail(sub.Email, sub.CancelToken); err != nil {
		response.InternalError(c, err)
		return
	}
	response.Created(c, gin.H{
		"email":        sub.Email,
		"cancel_token": sub.CancelToken, // return for dev/testing
	})
}

func (h *Handler) sendVerifyEmail(to, token string) error {
	if h.cfgSvc == nil {
		return nil
	}
	cfg, err := h.cfgSvc.Get()
	if err != nil {
		return err
	}
	if cfg == nil || !cfg.MailOptions.Enable {
		return nil
	}

	baseURL := firstNonEmpty(cfg.URL.ServerURL, cfg.URL.WebURL)
	verifyURL, err := buildVerifyURL(baseURL, token)
	if err != nil {
		return err
	}

	mailCfg := pkgmail.BuildMailConfig(cfg)
	sender := pkgmail.New(mailCfg)
	return sender.SendSubscribeVerify(to, pkgmail.SubscribeVerifyData{
		VerifyURL: verifyURL,
	})
}

func buildVerifyURL(baseURL, token string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("subscribe verify url is not configured")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid subscribe verify base url")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v2/subscribe/verify"
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (h *Handler) verify(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		response.BadRequest(c, "missing token")
		return
	}
	if err := h.svc.Verify(token); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, gin.H{"message": "subscription verified"})
}

func (h *Handler) unsubscribe(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		token = c.Query("cancelToken")
	}
	email := c.Query("email")

	var err error
	if token != "" {
		err = h.svc.Unsubscribe(token)
	} else if email != "" {
		err = h.svc.UnsubscribeByEmail(email)
	} else {
		response.BadRequest(c, "missing token or email")
		return
	}
	if err != nil {
		response.NotFoundMsg(c, err.Error())
		return
	}
	response.OK(c, gin.H{"message": "unsubscribed"})
}

func (h *Handler) list(c *gin.Context) {
	var subs []models.SubscribeModel
	if err := h.svc.db.Order("created_at DESC").Find(&subs).Error; err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, gin.H{"data": subs})
}

func (h *Handler) status(c *gin.Context) {
	bitMap := map[string]int{
		"post_c":     SubscribePostCreateBit,
		"note_c":     SubscribeNoteCreateBit,
		"say_c":      SubscribeSayCreateBit,
		"recently_c": SubscribeRecentCreateBit,
		"all":        SubscribeAllBit,
	}
	allowTypes := []string{"note_c", "post_c"}
	enabled, err := h.isSubscribeEnabled()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, gin.H{
		"enable":      enabled,
		"bit_map":     bitMap,
		"allow_types": allowTypes,
		"allow_bits":  []int{SubscribeNoteCreateBit, SubscribePostCreateBit},
	})
}

func (h *Handler) unsubscribeBatch(c *gin.Context) {
	var body struct {
		Emails []string `json:"emails"`
		All    bool     `json:"all"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	deletedCount, err := h.svc.BatchUnsubscribe(body.Emails, body.All)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.OK(c, gin.H{"deletedCount": deletedCount})
}

func normalizeSubscribe(dto *SubscribeDTO) int {
	mask, _ := normalizeSubscribeWithValidation(dto)
	return mask
}

func normalizeSubscribeWithValidation(dto *SubscribeDTO) (mask int, hasInvalidType bool) {
	if dto == nil {
		return 0, false
	}
	if dto.Subscribe != nil && *dto.Subscribe > 0 {
		return *dto.Subscribe, false
	}
	for _, t := range dto.Types {
		key := strings.ToLower(strings.TrimSpace(t))
		if key == "" {
			continue
		}
		if bit, ok := subscribeTypeToBitMap[key]; ok {
			mask |= bit
		} else {
			hasInvalidType = true
		}
	}
	return mask, hasInvalidType
}

func (h *Handler) isSubscribeEnabled() (bool, error) {
	if h.cfgSvc == nil {
		return true, nil
	}
	cfg, err := h.cfgSvc.Get()
	if err != nil {
		return false, err
	}
	if cfg == nil {
		return true, nil
	}
	return cfg.FeatureList.EmailSubscribe && cfg.MailOptions.Enable, nil
}
