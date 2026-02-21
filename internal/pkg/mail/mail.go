package mail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

// Config holds mail provider settings (matches FullConfig.Mail).
type Config struct {
	Enable    bool   `json:"enable"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	Pass      string `json:"pass"`
	From      string `json:"from"`
	ReplyTo   string `json:"reply_to"`
	UseResend bool   `json:"use_resend"`
	ResendKey string `json:"resend_key"`
}

// Message is a single email to send.
type Message struct {
	To      []string
	Subject string
	HTML    string
	Text    string
}

// Sender sends emails via SMTP or Resend.
type Sender struct {
	cfg Config
}

func New(cfg Config) *Sender {
	return &Sender{cfg: cfg}
}

// Send dispatches an email. Uses Resend if configured, otherwise SMTP.
func (s *Sender) Send(msg Message) error {
	if !s.cfg.Enable {
		return nil
	}
	if s.cfg.UseResend && s.cfg.ResendKey != "" {
		return s.sendResend(msg)
	}
	return s.sendSMTP(msg)
}

// sendSMTP sends via net/smtp.
func (s *Sender) sendSMTP(msg Message) error {
	host := s.cfg.Host
	port := s.cfg.Port
	if port == 0 {
		port = 587
	}
	addr := fmt.Sprintf("%s:%d", host, port)

	from := s.cfg.From
	if from == "" {
		from = s.cfg.User
	}

	var body bytes.Buffer
	body.WriteString("MIME-Version: 1.0\r\n")
	body.WriteString(fmt.Sprintf("From: %s\r\n", from))
	body.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(msg.To, ", ")))
	body.WriteString(fmt.Sprintf("Subject: %s\r\n", msg.Subject))
	body.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	if s.cfg.ReplyTo != "" {
		body.WriteString(fmt.Sprintf("Reply-To: %s\r\n", s.cfg.ReplyTo))
	}
	body.WriteString("\r\n")
	body.WriteString(msg.HTML)

	auth := smtp.PlainAuth("", s.cfg.User, s.cfg.Pass, host)
	return smtp.SendMail(addr, auth, from, msg.To, body.Bytes())
}

// sendResend sends via the Resend HTTP API.
func (s *Sender) sendResend(msg Message) error {
	from := s.cfg.From
	if from == "" {
		from = s.cfg.User
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"from":    from,
		"to":      msg.To,
		"subject": msg.Subject,
		"html":    msg.HTML,
	})

	req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.ResendKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var errResp struct {
			Message string `json:"message"`
		}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("resend error %d: %s", resp.StatusCode, errResp.Message)
	}
	return nil
}

const commentNotifyTpl = `<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;background:#f5f5f5;padding:20px">
<div style="max-width:600px;margin:0 auto;background:#fff;border-radius:8px;padding:24px">
  <h2 style="color:#333">新评论通知</h2>
  <p>您的文章 <strong>{{.Title}}</strong> 收到了新评论：</p>
  <blockquote style="border-left:4px solid #eee;padding-left:12px;color:#666">{{.Content}}</blockquote>
  <p>来自：{{.Author}}（{{.Mail}}）</p>
  {{if .ReplyContent}}<p>回复了：<em>{{.ReplyContent}}</em></p>{{end}}
  <p style="margin-top:24px">
    <a href="{{.ArticleURL}}" style="background:#4f46e5;color:#fff;padding:8px 16px;text-decoration:none;border-radius:4px">查看文章</a>
  </p>
</div>
</body>
</html>`

const subscribeVerifyTpl = `<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;background:#f5f5f5;padding:20px">
<div style="max-width:600px;margin:0 auto;background:#fff;border-radius:8px;padding:24px">
  <h2 style="color:#333">订阅验证</h2>
  <p>感谢您订阅！请点击下方按钮完成邮箱验证：</p>
  <p style="margin-top:24px">
    <a href="{{.VerifyURL}}" style="background:#4f46e5;color:#fff;padding:8px 16px;text-decoration:none;border-radius:4px">验证邮箱</a>
  </p>
  <p style="color:#999;font-size:12px">如果不是您本人操作，请忽略此邮件。</p>
</div>
</body>
</html>`

const replyNotifyTpl = `<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;background:#f5f5f5;padding:20px">
<div style="max-width:600px;margin:0 auto;background:#fff;border-radius:8px;padding:24px">
  <h2 style="color:#333">您的评论收到了回复</h2>
  <p>您在 <strong>{{.Title}}</strong> 的评论收到了回复：</p>
  <blockquote style="border-left:4px solid #eee;padding-left:12px;color:#666;font-style:italic">您的评论：{{.OriginalContent}}</blockquote>
  <p>回复内容：</p>
  <blockquote style="border-left:4px solid #4f46e5;padding-left:12px;color:#333">{{.ReplyContent}}</blockquote>
  <p style="margin-top:24px">
    <a href="{{.ArticleURL}}" style="background:#4f46e5;color:#fff;padding:8px 16px;text-decoration:none;border-radius:4px">查看文章</a>
  </p>
</div>
</body>
</html>`

// CommentNotifyData is the data for comment notification emails.
type CommentNotifyData struct {
	Title        string
	Content      string
	Author       string
	Mail         string
	ArticleURL   string
	ReplyContent string
}

// SubscribeVerifyData is the data for subscription verification emails.
type SubscribeVerifyData struct {
	VerifyURL string
}

// ReplyNotifyData is the data for reply notification emails.
type ReplyNotifyData struct {
	Title           string
	OriginalContent string
	ReplyContent    string
	ArticleURL      string
}

func renderTemplate(tpl string, data interface{}) (string, error) {
	t, err := template.New("").Parse(tpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// SendCommentNotify sends a new-comment notification to the admin.
func (s *Sender) SendCommentNotify(to string, data CommentNotifyData) error {
	html, err := renderTemplate(commentNotifyTpl, data)
	if err != nil {
		return err
	}
	return s.Send(Message{
		To:      []string{to},
		Subject: fmt.Sprintf("新评论：%s", data.Title),
		HTML:    html,
	})
}

// SendSubscribeVerify sends a verification email to a new subscriber.
func (s *Sender) SendSubscribeVerify(to string, data SubscribeVerifyData) error {
	html, err := renderTemplate(subscribeVerifyTpl, data)
	if err != nil {
		return err
	}
	return s.Send(Message{
		To:      []string{to},
		Subject: "请验证您的订阅邮箱",
		HTML:    html,
	})
}

// SendReplyNotify sends a reply notification to the original commenter.
func (s *Sender) SendReplyNotify(to string, data ReplyNotifyData) error {
	html, err := renderTemplate(replyNotifyTpl, data)
	if err != nil {
		return err
	}
	return s.Send(Message{
		To:      []string{to},
		Subject: fmt.Sprintf("您在「%s」的评论收到了回复", data.Title),
		HTML:    html,
	})
}
