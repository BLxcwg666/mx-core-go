package database

import (
	"fmt"

	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB is the global database instance.
var DB *gorm.DB

// Connect opens a MySQL connection and runs auto-migration.
func Connect(cfg *config.AppConfig) (*gorm.DB, error) {
	logLevel := logger.Warn
	if cfg.IsDev() {
		logLevel = logger.Info
	}

	db, err := gorm.Open(mysql.New(mysql.Config{
		DSN:               cfg.DSN,
		DefaultStringSize: 191,
	}), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("database connection failed: %w", err)
	}

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	DB = db
	return db, nil
}

// migrate runs GORM auto-migration for all models.
func migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&models.UserModel{},
		&models.UserSession{},
		&models.APIToken{},
		&models.OAuth2Token{},
		&models.AuthnModel{},
		&models.ReaderModel{},
		&models.CategoryModel{},
		&models.TopicModel{},
		&models.PostModel{},
		&models.NoteModel{},
		&models.PageModel{},
		&models.CommentModel{},
		&models.RecentlyModel{},
		&models.DraftModel{},
		&models.DraftHistoryModel{},
		&models.AISummaryModel{},
		&models.AIDeepReadingModel{},
		&models.AnalyzeModel{},
		&models.ActivityModel{},
		&models.SlugTrackerModel{},
		&models.FileReferenceModel{},
		&models.WebhookModel{},
		&models.WebhookEventModel{},
		&models.SnippetModel{},
		&models.ProjectModel{},
		&models.LinkModel{},
		&models.SayModel{},
		&models.SubscribeModel{},
		&models.MetaPresetModel{},
		&models.ServerlessStorageModel{},
		&models.OptionModel{},
	)
}
