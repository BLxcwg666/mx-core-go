package database

import (
	"fmt"

	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/pkg/cluster"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB is the global database instance.
var DB *gorm.DB

// Connect opens a MySQL connection and optionally runs auto-migration.
func Connect(cfg *config.AppConfig, autoMigrate bool) (*gorm.DB, error) {
	db, err := openDB(cfg, resolveLogLevel(cfg))
	if err != nil {
		return nil, err
	}

	if autoMigrate {
		if err := migrate(db); err != nil {
			return nil, fmt.Errorf("migration failed: %w", err)
		}
	}

	DB = db
	return db, nil
}

// EnsureSchema applies database migration in a short-lived setup connection.
func EnsureSchema(cfg *config.AppConfig) error {
	db, err := openDB(cfg, resolveLogLevel(cfg))
	if err != nil {
		return err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("resolve sql db: %w", err)
	}
	defer sqlDB.Close()

	if err := migrate(db); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}
	return nil
}

func resolveLogLevel(cfg *config.AppConfig) logger.LogLevel {
	logLevel := logger.Warn
	if cfg.IsDev() {
		if cluster.ShouldLogDevDiagnostics() {
			logLevel = logger.Info
		} else {
			logLevel = logger.Silent
		}
	}
	return logLevel
}

func openDB(cfg *config.AppConfig, logLevel logger.LogLevel) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.New(mysql.Config{
		DSN:               cfg.DSN,
		DefaultStringSize: 191,
	}), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("database connection failed: %w", err)
	}
	return db, nil
}

// migrate runs GORM auto-migration for all models.
func migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
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
	); err != nil {
		return err
	}

	if db.Dialector.Name() == "mysql" {
		if err := db.Exec("ALTER TABLE `analyzes` MODIFY COLUMN `ua` LONGTEXT NULL").Error; err != nil {
			return err
		}
		if err := db.Exec("ALTER TABLE `posts` MODIFY COLUMN `images` LONGTEXT NULL").Error; err != nil {
			return err
		}
		if err := db.Exec("ALTER TABLE `notes` MODIFY COLUMN `images` LONGTEXT NULL").Error; err != nil {
			return err
		}
		if err := db.Exec("ALTER TABLE `pages` MODIFY COLUMN `images` LONGTEXT NULL").Error; err != nil {
			return err
		}
		if err := db.Exec("ALTER TABLE `activities` MODIFY COLUMN `payload` LONGTEXT NULL").Error; err != nil {
			return err
		}
		if err := db.Exec("ALTER TABLE `meta_presets` MODIFY COLUMN `options` LONGTEXT NULL").Error; err != nil {
			return err
		}
		if err := db.Exec("ALTER TABLE `meta_presets` MODIFY COLUMN `children` LONGTEXT NULL").Error; err != nil {
			return err
		}
	}

	return nil
}
