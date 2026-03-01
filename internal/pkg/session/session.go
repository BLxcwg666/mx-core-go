package session

import (
	"strings"
	"time"

	"github.com/mx-space/core/internal/models"
	jwtpkg "github.com/mx-space/core/internal/pkg/jwt"
	"gorm.io/gorm"
)

const DefaultTTL = 30 * 24 * time.Hour

// Issue creates a DB session and signs a JWT bound to that session.
func Issue(db *gorm.DB, userID, ip, ua string, ttl time.Duration) (string, *models.UserSession, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}

	now := time.Now()
	s := &models.UserSession{
		UserID:    userID,
		IP:        strings.TrimSpace(ip),
		UA:        strings.TrimSpace(ua),
		ExpiresAt: now.Add(ttl),
	}
	if err := db.Create(s).Error; err != nil {
		return "", nil, err
	}

	token, err := jwtpkg.SignWithOptions(userID, ttl, jwtpkg.SignOptions{
		SessionID: s.ID,
		IP:        s.IP,
		UA:        s.UA,
	})
	if err != nil {
		_ = db.Delete(s).Error
		return "", nil, err
	}
	return token, s, nil
}

func IsActive(db *gorm.DB, userID, sessionID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		// Legacy token without sid.
		return true, nil
	}

	var count int64
	err := db.Model(&models.UserSession{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL AND expires_at > ?", sessionID, userID, time.Now()).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func Touch(db *gorm.DB, userID, sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	_ = db.Model(&models.UserSession{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL AND expires_at > ?", sessionID, userID, time.Now()).
		Update("updated_at", time.Now()).Error
}

func ListActive(db *gorm.DB, userID string) ([]models.UserSession, error) {
	var sessions []models.UserSession
	err := db.Where("user_id = ? AND revoked_at IS NULL AND expires_at > ?", userID, time.Now()).
		Order("updated_at DESC, created_at DESC").
		Find(&sessions).Error
	return sessions, err
}

func Revoke(db *gorm.DB, userID, sessionID string) error {
	now := time.Now()
	res := db.Model(&models.UserSession{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", sessionID, userID).
		Update("revoked_at", &now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func RevokeAfter(db *gorm.DB, userID, sessionID string, delay time.Duration) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if delay <= 0 {
		_ = Revoke(db, userID, sessionID)
		return
	}
	time.AfterFunc(delay, func() {
		_ = Revoke(db, userID, sessionID)
	})
}

func RevokeAllExcept(db *gorm.DB, userID, keepSessionID string) error {
	now := time.Now()
	query := db.Model(&models.UserSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID)
	if strings.TrimSpace(keepSessionID) != "" {
		query = query.Where("id <> ?", keepSessionID)
	}
	return query.Update("revoked_at", &now).Error
}
