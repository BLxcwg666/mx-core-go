package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mx-space/core/internal/models"
	"gorm.io/gorm"
)

func (h *Handler) cacheGet(namespace, key string) interface{} {
	if key == "" {
		return nil
	}
	cacheKey := serverlessCacheKeyPrefix + namespace + ":" + key

	raw, err := h.rc.Get(context.Background(), cacheKey)
	if err != nil || raw == "" {
		return nil
	}
	return decodeStorageValue(raw)
}

func (h *Handler) cacheSet(namespace, key string, value interface{}, ttlSeconds int64) {
	if key == "" {
		return
	}
	cacheKey := serverlessCacheKeyPrefix + namespace + ":" + key
	if ttlSeconds <= 0 {
		ttlSeconds = int64((7 * 24 * time.Hour).Seconds())
	}

	_ = h.rc.Set(
		context.Background(),
		cacheKey,
		encodeStorageValue(value),
		time.Duration(ttlSeconds)*time.Second,
	)
}

func (h *Handler) cacheDel(namespace, key string) {
	if key == "" {
		return
	}
	cacheKey := serverlessCacheKeyPrefix + namespace + ":" + key

	_ = h.rc.Del(context.Background(), cacheKey)
}

func (h *Handler) storageGet(namespace, key string) interface{} {
	if key == "" {
		return nil
	}
	var item models.ServerlessStorageModel
	err := h.db.
		Where("namespace = ? AND `key` = ?", namespace, key).
		First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return nil
	}
	return decodeStorageValue(item.Value)
}

func (h *Handler) storageFind(namespace string, condition interface{}) interface{} {
	keyFilter := ""
	if cond, ok := condition.(map[string]interface{}); ok {
		keyFilter = strings.TrimSpace(toString(cond["key"]))
	}

	tx := h.db.
		Model(&models.ServerlessStorageModel{}).
		Where("namespace = ?", namespace).
		Order("created_at DESC")
	if keyFilter != "" {
		tx = tx.Where("`key` = ?", keyFilter)
	}

	var items []models.ServerlessStorageModel
	if err := tx.Find(&items).Error; err != nil {
		return []interface{}{}
	}

	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]interface{}{
			"id":    item.ID,
			"key":   item.Key,
			"value": decodeStorageValue(item.Value),
		})
	}
	return out
}

func (h *Handler) storageSet(namespace, key string, value interface{}) {
	if key == "" {
		return
	}
	encoded := encodeStorageValue(value)

	var existing models.ServerlessStorageModel
	err := h.db.
		Where("namespace = ? AND `key` = ?", namespace, key).
		First(&existing).Error
	if err == nil {
		_ = h.db.Model(&existing).Update("value", encoded).Error
		return
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return
	}

	record := models.ServerlessStorageModel{
		Namespace: namespace,
		Key:       key,
		Value:     encoded,
	}
	_ = h.db.Create(&record).Error
}

func (h *Handler) storageInsert(namespace, key string, value interface{}) error {
	if key == "" {
		return errors.New("key is required")
	}

	var existing models.ServerlessStorageModel
	err := h.db.
		Where("namespace = ? AND `key` = ?", namespace, key).
		First(&existing).Error
	if err == nil {
		return errors.New("key already exists")
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}

	record := models.ServerlessStorageModel{
		Namespace: namespace,
		Key:       key,
		Value:     encodeStorageValue(value),
	}
	return h.db.Create(&record).Error
}

func (h *Handler) storageUpdate(namespace, key string, value interface{}) error {
	if key == "" {
		return errors.New("key is required")
	}

	var existing models.ServerlessStorageModel
	err := h.db.
		Where("namespace = ? AND `key` = ?", namespace, key).
		First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		return errors.New("key not exists")
	}
	if err != nil {
		return err
	}
	return h.db.Model(&existing).Update("value", encodeStorageValue(value)).Error
}

func (h *Handler) storageDel(namespace, key string) {
	if key == "" {
		return
	}
	_ = h.db.
		Where("namespace = ? AND `key` = ?", namespace, key).
		Delete(&models.ServerlessStorageModel{}).Error
}

func encodeStorageValue(v interface{}) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}

func decodeStorageValue(raw string) interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err == nil {
		return v
	}
	return raw
}
