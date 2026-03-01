package configs

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service manages the persisted FullConfig.
type Service struct {
	db     *gorm.DB
	mu     sync.RWMutex
	cfg    *config.FullConfig
	logger *zap.Logger
}

func NewService(db *gorm.DB, opts ...Option) *Service {
	s := &Service{db: db, logger: zap.NewNop()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Option configures a Service.
type Option func(*Service)

// WithLogger sets the logger for the configs service.
func WithLogger(l *zap.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l.Named("ConfigsService")
		}
	}
}

// Get returns the current config, loading from DB if not cached.
func (s *Service) Get() (*config.FullConfig, error) {
	s.mu.RLock()
	if s.cfg != nil {
		defer s.mu.RUnlock()
		return s.cfg, nil
	}
	s.mu.RUnlock()

	return s.load()
}

func (s *Service) load() (*config.FullConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var opt models.OptionModel
	err := s.db.Where("name = ?", configKey).First(&opt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		defaults := config.DefaultFullConfig()
		s.cfg = &defaults
		_ = s.persist(&defaults)
		s.logger.Info("Config 已经加载完毕！（使用默认配置）")
		return s.cfg, nil
	}
	if err != nil {
		s.logger.Warn("获取配置失败", zap.Error(err))
		return nil, err
	}

	cfg := config.DefaultFullConfig()
	if err := json.Unmarshal([]byte(opt.Value), &cfg); err != nil {
		s.logger.Warn("获取配置失败", zap.Error(err))
		return nil, err
	}
	s.cfg = &cfg
	s.logger.Info("Config 已经加载完毕！")
	return s.cfg, nil
}

// Patch merges the given partial JSON update into the current config and persists it.
func (s *Service) Patch(partial map[string]json.RawMessage) (*config.FullConfig, error) {
	current, err := s.Get()
	if err != nil {
		return nil, err
	}

	currentJSON, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}
	merged := map[string]interface{}{}
	if err := json.Unmarshal(currentJSON, &merged); err != nil {
		return nil, err
	}
	for key, section := range merged {
		merged[key] = normalizeConfigSection(key, section)
	}

	for k, v := range partial {
		if len(strings.TrimSpace(string(v))) == 0 {
			continue
		}
		var incoming interface{}
		if err := json.Unmarshal(v, &incoming); err != nil {
			return nil, err
		}
		incoming = normalizeConfigSection(k, incoming)
		if existing, ok := merged[k]; ok {
			merged[k] = deepMergeJSON(existing, incoming)
			continue
		}
		merged[k] = incoming
	}

	mergedJSON, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}

	updated := config.DefaultFullConfig()
	if err := json.Unmarshal(mergedJSON, &updated); err != nil {
		return nil, err
	}
	if shouldEnableCommentAIReview(partial) &&
		updated.CommentOptions.AIReview &&
		!hasEnabledAIProvider(updated.AI.Providers) {
		return nil, errAIReviewProviderNotEnabled
	}

	s.mu.Lock()
	s.cfg = &updated
	s.mu.Unlock()

	return &updated, s.persist(&updated)
}

func (s *Service) persist(cfg *config.FullConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	opt := models.OptionModel{Name: configKey, Value: string(data)}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&opt).Error
}

// Invalidate clears the in-memory config cache, forcing a DB reload on next Get.
func (s *Service) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = nil
}
