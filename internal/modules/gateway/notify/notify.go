package notify

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/mx-space/core/internal/config"
	"github.com/mx-space/core/internal/models"
	"github.com/mx-space/core/internal/modules/gateway/webhook"
	"github.com/mx-space/core/internal/modules/syndication/subscribe"
	appconfigs "github.com/mx-space/core/internal/modules/system/core/configs"
	"github.com/mx-space/core/internal/pkg/bark"
	pkgmail "github.com/mx-space/core/internal/pkg/mail"
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
}

// New creates a new notification service.
func New(db *gorm.DB, cfgSvc *appconfigs.Service, webhookSvc *webhook.Service, barkSvc *bark.Service, subscribeSvc *subscribe.Service) *Service {
	return &Service{
		db:           db,
		cfgSvc:       cfgSvc,
		webhookSvc:   webhookSvc,
		barkSvc:      barkSvc,
		subscribeSvc: subscribeSvc,
	}
}

// SetImageSync sets an optional function to sync images on content publish.
func (s *Service) SetImageSync(fn func(contentID, contentType string) error) {
	s.imageSyncFn = fn
}

// OnCommentCreate is called when a non-admin user creates a comment.
// It notifies the blog owner via email and Bark, and dispatches a webhook.
func (s *Service) OnCommentCreate(cm *models.CommentModel) {
	cfg, err := s.cfgSvc.Get()
	if err != nil || cfg == nil {
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
		_ = s.barkSvc.Push(title, body)
	}

	// Email notification to admin.
	if cfg.MailOptions.Enable && masterMail != "" {
		refTitle := s.getRefTitle(cm.RefType, cm.RefID)
		articleURL := s.buildRefURL(cfg, cm.RefType, cm.RefID)
		sender := pkgmail.New(pkgmail.BuildMailConfig(cfg))
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
	if err != nil || cfg == nil {
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
		articleURL := s.buildRefURL(cfg, reply.RefType, reply.RefID)
		sender := pkgmail.New(pkgmail.BuildMailConfig(cfg))
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
	if err != nil || cfg == nil {
		return
	}

	if s.webhookSvc != nil {
		s.webhookSvc.Dispatch("POST_CREATE", post)
	}

	// Sync images to S3 if configured.
	if s.imageSyncFn != nil {
		_ = s.imageSyncFn(post.ID, "post")
	}

	detailURL := s.buildPostURL(cfg, post)
	s.sendNewsletter(cfg, post.Title, post.Text, detailURL, subscribe.SubscribePostCreateBit)
}

// OnNoteCreate is called when a new note is published.
// It sends newsletters to subscribers and dispatches a webhook.
func (s *Service) OnNoteCreate(note *models.NoteModel) {
	cfg, err := s.cfgSvc.Get()
	if err != nil || cfg == nil {
		return
	}

	if s.webhookSvc != nil {
		s.webhookSvc.Dispatch("NOTE_CREATE", note)
	}

	// Sync images to S3 if configured.
	if s.imageSyncFn != nil {
		_ = s.imageSyncFn(note.ID, "note")
	}

	webURL := strings.TrimRight(cfg.URL.WebURL, "/")
	detailURL := fmt.Sprintf("%s/notes/%d", webURL, note.NID)
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
	if err != nil || len(subs) == 0 {
		return
	}

	master, _, masterAvatar := s.getMasterInfo()
	webURL := strings.TrimRight(cfg.URL.WebURL, "/")

	// Truncate text for newsletter preview.
	preview := text
	if len(preview) > 300 {
		preview = preview[:300] + "..."
	}

	sender := pkgmail.New(pkgmail.BuildMailConfig(cfg))
	for _, sub := range subs {
		unsubURL := fmt.Sprintf("%s/api/v2/subscribe/unsubscribe?token=%s", webURL, sub.CancelToken)
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
