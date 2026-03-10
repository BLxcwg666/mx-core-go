package notify

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/gateway/webhook"
	"github.com/mx-space/core/internal/modules/syndication/subscribe"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	"github.com/mx-space/core/internal/pkg/bark"
	pkgmail "github.com/mx-space/core/internal/pkg/mail"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service orchestrates all notification channels (email, Bark, webhook, newsletter).
type Service struct {
	db           *gorm.DB
	cfgSvc       *appconfigs.Service
	webhookSvc   *webhook.Service
	barkSvc      *bark.Service
	subscribeSvc *subscribe.Service
	imageSyncFn  func(contentID, contentType string) error
	logger       *zap.Logger
}

// New creates a new notification service.
func New(db *gorm.DB, cfgSvc *appconfigs.Service, webhookSvc *webhook.Service, barkSvc *bark.Service, subscribeSvc *subscribe.Service, opts ...Option) *Service {
	s := &Service{
		db:           db,
		cfgSvc:       cfgSvc,
		webhookSvc:   webhookSvc,
		barkSvc:      barkSvc,
		subscribeSvc: subscribeSvc,
		logger:       zap.NewNop(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Option configures a notification service.
type Option func(*Service)

// WithLogger sets the logger for the notification service.
func WithLogger(l *zap.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l.Named("NotifyService")
		}
	}
}

// SetImageSync sets an optional function to sync images on content publish.
func (s *Service) SetImageSync(fn func(contentID, contentType string) error) {
	s.imageSyncFn = fn
}

// OnCommentCreate is called when a non-admin user creates a comment.
// It notifies the blog owner via email and Bark, and dispatches a webhook.
func (s *Service) OnCommentCreate(cm *models.CommentModel, sendOwnerEmail bool) {
	cfg, err := s.cfgSvc.Get()
	if err != nil {
		s.logger.Warn("load config for comment notification failed", zap.Error(err))
		return
	}
	if cfg == nil {
		return
	}

	// Dispatch webhook.
	if s.webhookSvc != nil {
		s.webhookSvc.Dispatch("COMMENT_CREATE", cm)
	}

	master, masterMail, masterAvatar := s.getMasterInfo()

	// Bark push notification.
	if s.barkSvc != nil && cfg.BarkOptions.Enable && cfg.BarkOptions.EnableComment {
		title := fmt.Sprintf("收到新评论 (来自 %s)", cm.Author)
		body := cm.Text
		if len(body) > 100 {
			body = body[:100] + "..."
		}
		if err := s.barkSvc.Push(title, body); err != nil {
			s.logger.Warn("comment bark notification failed", zap.String("author", cm.Author), zap.Error(err))
		}
	}

	// Email notification to admin.
	if sendOwnerEmail && cfg.MailOptions.Enable && masterMail != "" {
		refTitle := s.getRefTitle(cm.RefType, cm.RefID)
		articleURL := s.buildCommentURL(cfg, cm.RefType, cm.RefID, cm.ID)
		sender := pkgmail.New(pkgmail.BuildMailConfig(cfg), pkgmail.WithLogger(s.logger))
		_ = sender.SendCommentNotify(masterMail, pkgmail.CommentNotifyData{
			Title:       refTitle,
			Content:     cm.Text,
			Author:      cm.Author,
			Mail:        cm.Mail,
			ArticleURL:  articleURL,
			Master:      master,
			IP:          cm.IP,
			Agent:       cm.Agent,
			URL:         cm.URL,
			OwnerAvatar: masterAvatar,
			SiteName:    cfg.SEO.Title,
		})
	}
}

// OnMasterReply is called when the admin replies to a comment.
// It notifies the original commenter via email and dispatches a webhook.
func (s *Service) OnMasterReply(reply *models.CommentModel, parent *models.CommentModel) {
	cfg, err := s.cfgSvc.Get()
	if err != nil {
		s.logger.Warn("load config for reply notification failed", zap.Error(err))
		return
	}
	if cfg == nil {
		return
	}

	// Dispatch webhook.
	if s.webhookSvc != nil {
		s.webhookSvc.Dispatch("COMMENT_CREATE", reply)
	}

	// Email notification to original commenter.
	parentMail := strings.TrimSpace(parent.Mail)
	if cfg.MailOptions.Enable && parentMail != "" {
		master, _, masterAvatar := s.getMasterInfo()
		refTitle := s.getRefTitle(reply.RefType, reply.RefID)
		articleURL := s.buildCommentURL(cfg, reply.RefType, reply.RefID, reply.ID)
		sender := pkgmail.New(pkgmail.BuildMailConfig(cfg), pkgmail.WithLogger(s.logger))
		_ = sender.SendReplyNotify(parentMail, pkgmail.ReplyNotifyData{
			Title:           refTitle,
			OriginalContent: parent.Text,
			ReplyContent:    reply.Text,
			ArticleURL:      articleURL,
			Master:          master,
			OwnerAvatar:     masterAvatar,
			SiteName:        cfg.SEO.Title,
		})
	}
}

// OnPostCreate is called when a new post is published.
// It sends newsletters to subscribers and dispatches a webhook.
func (s *Service) OnPostCreate(post *models.PostModel) {
	cfg, err := s.cfgSvc.Get()
	if err != nil {
		s.logger.Warn("load config for post notification failed", zap.Error(err))
		return
	}
	if cfg == nil {
		return
	}

	if s.webhookSvc != nil {
		s.webhookSvc.Dispatch("POST_CREATE", post)
	}

	// Sync images to S3 if configured.
	if s.imageSyncFn != nil {
		if err := s.imageSyncFn(post.ID, "post"); err != nil {
			s.logger.Warn("post image sync failed", zap.String("id", post.ID), zap.Error(err))
		}
	}

	detailURL := s.buildPostURL(cfg, post)
	s.sendNewsletter(cfg, post.Title, post.Text, detailURL, subscribe.SubscribePostCreateBit)
}

// OnNoteCreate is called when a new note is published.
// It sends newsletters to subscribers and dispatches a webhook.
func (s *Service) OnNoteCreate(note *models.NoteModel) {
	cfg, err := s.cfgSvc.Get()
	if err != nil {
		s.logger.Warn("load config for note notification failed", zap.Error(err))
		return
	}
	if cfg == nil {
		return
	}

	if s.webhookSvc != nil {
		s.webhookSvc.Dispatch("NOTE_CREATE", note)
	}

	// Sync images to S3 if configured.
	if s.imageSyncFn != nil {
		if err := s.imageSyncFn(note.ID, "note"); err != nil {
			s.logger.Warn("note image sync failed", zap.String("id", note.ID), zap.Error(err))
		}
	}

	webURL := strings.TrimRight(cfg.URL.WebURL, "/")
	detailURL := fmt.Sprintf("%s/notes/%d", webURL, note.NID)
	if strings.TrimSpace(note.Password) != "" {
		return
	}
	if note.PublicAt != nil && note.PublicAt.After(time.Now()) {
		return
	}
	s.sendNewsletter(cfg, note.Title, note.Text, detailURL, subscribe.SubscribeNoteCreateBit)
}

func (s *Service) sendNewsletter(cfg *config.FullConfig, title, text, detailURL string, bit int) {
	if !cfg.MailOptions.Enable || !cfg.FeatureList.EmailSubscribe {
		return
	}
	if s.subscribeSvc == nil {
		return
	}
	subs, err := s.subscribeSvc.GetSubscribers(bit)
	if err != nil {
		s.logger.Warn("load subscribers failed", zap.Int("bit", bit), zap.Error(err))
		return
	}
	if len(subs) == 0 {
		return
	}

	master, _, masterAvatar := s.getMasterInfo()

	// Truncate text for newsletter preview.
	preview := text
	if len(preview) > 300 {
		preview = preview[:300] + "..."
	}

	sender := pkgmail.New(pkgmail.BuildMailConfig(cfg), pkgmail.WithLogger(s.logger))
	for _, sub := range subs {
		unsubBaseURL := s.buildSubscribeActionURL(cfg, "/subscribe/unsubscribe")
		unsubURL := ""
		if unsubBaseURL != "" {
			unsubURL = fmt.Sprintf("%s?token=%s", unsubBaseURL, sub.CancelToken)
		}
		_ = sender.SendNewsletter(sub.Email, pkgmail.NewsletterData{
			OwnerName:      master,
			OwnerAvatar:    masterAvatar,
			Title:          title,
			Text:           preview,
			DetailURL:      detailURL,
			UnsubscribeURL: unsubURL,
			SiteName:       cfg.SEO.Title,
		})
	}
}

func (s *Service) buildCommentURL(cfg *config.FullConfig, refType models.RefType, refID, commentID string) string {
	articleURL := s.buildRefURL(cfg, refType, refID)
	if articleURL == "" || strings.TrimSpace(commentID) == "" {
		return articleURL
	}
	return articleURL + "#comments-" + commentID
}

// getMasterInfo returns (name, mail, avatar) for the first registered user (the master).
func (s *Service) getMasterInfo() (name, mail, avatar string) {
	var user models.UserModel
	if err := s.db.Select("name, mail, avatar").First(&user).Error; err != nil {
		return "Master", "", ""
	}
	name = user.Name
	if name == "" {
		name = "Master"
	}
	mail = user.Mail
	avatar = strings.TrimSpace(user.Avatar)
	if avatar == "" && mail != "" {
		sum := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(mail))))
		avatar = "https://avatar.xcnya.cn/avatar/" + hex.EncodeToString(sum[:]) + "?d=retro"
	}
	return
}

// getRefTitle fetches the title of the referenced content.
func (s *Service) getRefTitle(refType models.RefType, refID string) string {
	switch refType {
	case models.RefTypePost:
		var post models.PostModel
		if err := s.db.Select("title").First(&post, "id = ?", refID).Error; err == nil {
			return post.Title
		}
	case models.RefTypeNote:
		var note models.NoteModel
		if err := s.db.Select("title").First(&note, "id = ?", refID).Error; err == nil {
			return note.Title
		}
	case models.RefTypePage:
		var page models.PageModel
		if err := s.db.Select("title").First(&page, "id = ?", refID).Error; err == nil {
			return page.Title
		}
	}
	return "未知内容"
}

// buildRefURL constructs a URL to the referenced content item.
func (s *Service) buildRefURL(cfg *config.FullConfig, refType models.RefType, refID string) string {
	webURL := strings.TrimRight(cfg.URL.WebURL, "/")
	switch refType {
	case models.RefTypePost:
		var post models.PostModel
		if err := s.db.Select("id, slug, category_id").First(&post, "id = ?", refID).Error; err == nil {
			return s.buildPostURL(cfg, &post)
		}
		return webURL
	case models.RefTypeNote:
		var note models.NoteModel
		if err := s.db.Select("n_id").First(&note, "id = ?", refID).Error; err == nil {
			return fmt.Sprintf("%s/notes/%d", webURL, note.NID)
		}
		return webURL
	case models.RefTypePage:
		var page models.PageModel
		if err := s.db.Select("slug").First(&page, "id = ?", refID).Error; err == nil {
			return fmt.Sprintf("%s/%s", webURL, strings.TrimLeft(page.Slug, "/"))
		}
		return webURL
	default:
		return webURL
	}
}

func (s *Service) buildPostURL(cfg *config.FullConfig, post *models.PostModel) string {
	webURL := strings.TrimRight(cfg.URL.WebURL, "/")
	if post == nil {
		return webURL
	}

	categorySlug := ""
	if post.Category != nil {
		categorySlug = strings.TrimSpace(post.Category.Slug)
	}
	if categorySlug == "" && post.CategoryID != nil && strings.TrimSpace(*post.CategoryID) != "" {
		var category models.CategoryModel
		if err := s.db.Select("slug").First(&category, "id = ?", *post.CategoryID).Error; err == nil {
			categorySlug = strings.TrimSpace(category.Slug)
		}
	}
	if categorySlug == "" {
		categorySlug = "uncategorized"
	}
	return fmt.Sprintf("%s/posts/%s/%s", webURL, categorySlug, post.Slug)
}

func (s *Service) buildSubscribeActionURL(cfg *config.FullConfig, path string) string {
	if cfg == nil {
		return ""
	}
	baseURL := strings.TrimRight(firstNonEmpty(cfg.URL.ServerURL, cfg.URL.WebURL), "/")
	if baseURL == "" {
		return ""
	}
	return baseURL + "/api/v2" + path
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
