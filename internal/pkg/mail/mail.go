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
<html lang="zh-CN">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=UTF-8" />
</head>
<body style="background-color:#fff;margin:0 auto;font-family:ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Helvetica Neue,Arial,Noto Sans,sans-serif;padding:.5rem">
  <table align="center" width="100%" role="presentation" cellspacing="0" cellpadding="0" border="0" style="max-width:100%;border-width:1px;border-style:solid;border-radius:.25rem;box-shadow:0 4px 6px -1px rgb(0 0 0 / .1),0 2px 4px -2px rgb(0 0 0 / .1);margin:40px auto;padding:20px;width:550px;border-color:rgb(14,165,233);position:relative;overflow:hidden">
    <tbody>
      <tr><td>
        <table align="center" width="100%" role="presentation" border="0" cellpadding="0" cellspacing="0" style="text-align:center;margin-top:24px">
          <tbody><tr><td>
            <img src="{{.OwnerAvatar}}" style="display:block;outline:none;border:none;text-decoration:none;margin:0 auto;border-radius:.75rem;height:3rem;width:3rem" />
          </td></tr></tbody>
        </table>
        <h1 style="color:#000;font-size:18px;font-weight:400;text-align:center;margin:30px 0">『<strong>{{.Title}}</strong>』 的评论收到了回复</h1>
        <p style="font-size:14px;line-height:24px;margin:16px 0;color:#000"><strong>{{.Author}}</strong> 的回复：</p>
        <table align="center" width="100%" role="presentation" border="0" cellpadding="0" cellspacing="0" style="background-color:rgb(243,244,246);border-radius:.75rem;padding:0 1rem">
          <tbody><tr><td><p style="font-size:12px;line-height:24px;margin:16px 0;color:rgb(51,51,51)">{{.Content}}</p></td></tr></tbody>
        </table>
        {{if .ReplyContent}}
        <p style="font-size:14px;line-height:24px;margin:16px 0;color:#000">原评论：</p>
        <table align="center" width="100%" role="presentation" border="0" cellpadding="0" cellspacing="0" style="background-color:rgb(243,244,246);border-radius:.75rem;padding:0 1rem">
          <tbody><tr><td><p style="font-size:12px;line-height:24px;margin:16px 0;color:rgb(51,51,51)">{{.ReplyContent}}</p></td></tr></tbody>
        </table>
        {{end}}
        <p style="font-size:14px;line-height:24px;margin:16px 0;color:#000">其他信息：</p>
        <table align="center" width="100%" role="presentation" border="0" cellpadding="0" cellspacing="0" style="background-color:rgb(243,244,246);border-radius:.75rem;padding:0 1rem">
          <tbody><tr><td><p style="font-size:12px;line-height:24px;margin:16px 0;color:rgb(51,51,51)">IP: {{.IP}}<br />Mail: {{.Mail}}<br />Agent: {{.Agent}}<br />HomePage: {{.URL}}</p></td></tr></tbody>
        </table>
        <table align="center" width="100%" role="presentation" border="0" cellpadding="0" cellspacing="0" style="text-align:center;margin:32px 0">
          <tbody><tr><td>
            <a href="{{.ArticleURL}}" target="_blank" style="line-height:100%;text-decoration:none;display:inline-block;max-width:100%;padding:12px 20px;background-color:rgb(14,165,233);border-radius:.25rem;color:#fff;font-size:12px;font-weight:600;text-align:center">查看完整内容</a>
          </td></tr></tbody>
        </table>
        <hr style="width:100%;border:none;border-top:1px solid #eaeaea;margin:26px 0" />
        <p style="font-size:12px;line-height:24px;margin:16px 0;color:rgb(107,114,128)">萤火虫消失之后，那光的轨迹仍久久地印在我的脑际。那微弱浅淡的光点，仿佛迷失方向的魂灵，在漆黑厚重的夜幕中彷徨。——《挪威的森林》村上春树</p>
        <p style="font-size:10px;line-height:24px;margin:16px 0;text-align:center;color:rgb(156,163,175)">本邮件为系统自动发送，请勿直接回复~<br />©{{year}} Copyright {{.Master}}</p>
      </td></tr>
    </tbody>
  </table>
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
<html lang="zh-CN">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=UTF-8" />
</head>
<body style="background-color:#fff;margin:0 auto;font-family:ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Helvetica Neue,Arial,Noto Sans,sans-serif;padding:.5rem">
  <table align="center" width="100%" role="presentation" cellspacing="0" cellpadding="0" border="0" style="max-width:100%;border-width:1px;border-style:solid;border-radius:.25rem;box-shadow:0 4px 6px -1px rgb(0 0 0 / .1),0 2px 4px -2px rgb(0 0 0 / .1);margin:40px auto;padding:20px;width:550px;border-color:rgb(14,165,233);position:relative;overflow:hidden">
    <tbody>
      <tr><td>
        <table align="center" width="100%" role="presentation" border="0" cellpadding="0" cellspacing="0" style="text-align:center;margin-top:24px">
          <tbody><tr><td>
            <img src="{{.OwnerAvatar}}" style="display:block;outline:none;border:none;text-decoration:none;margin:0 auto;border-radius:.75rem;height:3rem;width:3rem" />
          </td></tr></tbody>
        </table>
        <h1 style="color:#000;font-size:18px;font-weight:400;text-align:center;margin:30px 0">您在 『<strong>{{.Title}}</strong>』 的评论有了新的回复呐~</h1>
        <p style="font-size:14px;line-height:24px;margin:16px 0;color:#000"><strong>{{.Master}}</strong> 给您的回复：</p>
        <table align="center" width="100%" role="presentation" border="0" cellpadding="0" cellspacing="0" style="background-color:rgb(243,244,246);border-radius:.75rem;padding:0 1rem">
          <tbody><tr><td><p style="font-size:12px;line-height:24px;margin:16px 0;color:rgb(51,51,51)">{{.ReplyContent}}</p></td></tr></tbody>
        </table>
        {{if .OriginalContent}}
        <p style="font-size:14px;line-height:24px;margin:16px 0;color:#000">你之前的回复是：</p>
        <table align="center" width="100%" role="presentation" border="0" cellpadding="0" cellspacing="0" style="background-color:rgb(243,244,246);border-radius:.75rem;padding:0 1rem">
          <tbody><tr><td><p style="font-size:12px;line-height:24px;margin:16px 0;color:rgb(51,51,51)">{{.OriginalContent}}</p></td></tr></tbody>
        </table>
        {{end}}
        <table align="center" width="100%" role="presentation" border="0" cellpadding="0" cellspacing="0" style="text-align:center;margin:32px 0">
          <tbody><tr><td>
            <a href="{{.ArticleURL}}" target="_blank" style="line-height:100%;text-decoration:none;display:inline-block;max-width:100%;padding:12px 20px;background-color:rgb(14,165,233);border-radius:.25rem;color:#fff;font-size:12px;font-weight:600;text-align:center">查看完整内容</a>
          </td></tr></tbody>
        </table>
        <hr style="width:100%;border:none;border-top:1px solid #eaeaea;margin:26px 0" />
        <p style="font-size:12px;line-height:24px;margin:16px 0;color:rgb(107,114,128)">萤火虫消失之后，那光的轨迹仍久久地印在我的脑际。那微弱浅淡的光点，仿佛迷失方向的魂灵，在漆黑厚重的夜幕中彷徨。——《挪威的森林》村上春树</p>
        <p style="font-size:10px;line-height:24px;margin:16px 0;text-align:center;color:rgb(156,163,175)">本邮件为系统自动发送，请勿直接回复~<br />©{{year}} Copyright {{.Master}}</p>
      </td></tr>
    </tbody>
  </table>
</body>
</html>`

const newsletterTpl = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta http-equiv="Content-Type" content="text/html; charset=UTF-8" />
</head>
<body style="background-color:#fff;margin:0 auto;font-family:ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Helvetica Neue,Arial,Noto Sans,sans-serif;padding:.5rem">
  <table align="center" width="100%" role="presentation" cellspacing="0" cellpadding="0" border="0" style="max-width:100%;border-radius:.375rem;box-shadow:0 4px 6px -1px rgb(0 0 0 / .1),0 2px 4px -2px rgb(0 0 0 / .1);margin:40px auto;padding:20px;width:550px;position:relative;overflow:hidden;border:1px solid rgb(251,113,133)">
    <tbody>
      <tr><td>
        <table align="center" width="100%" role="presentation" border="0" cellpadding="0" cellspacing="0" style="text-align:center;margin-top:24px">
          <tbody><tr><td>
            <img src="{{.OwnerAvatar}}" style="display:block;outline:none;border:none;text-decoration:none;margin:0 auto;border-radius:.75rem;height:3rem;width:3rem" />
          </td></tr></tbody>
        </table>
        <p style="font-size:14px;line-height:24px;margin:16px 0">你关注的 @{{.OwnerName}} 刚刚发布了：</p>
        <h1 style="font-size:20px;text-align:center">{{.Title}}</h1>
        <p style="font-size:14px;line-height:24px;margin:16px 0">{{.Text}}</p>
        <table align="center" width="100%" role="presentation" border="0" cellpadding="0" cellspacing="0" style="text-align:center;margin-top:32px;margin-bottom:32px;position:relative">
          <tbody><tr><td>
            <a href="{{.DetailURL}}" target="_blank" style="line-height:100%;text-decoration:none;display:inline-block;max-width:100%;padding:12px 20px;background-color:rgb(251,113,133);border-radius:.25rem;color:#fff;font-size:12px;font-weight:600;text-align:center">查看完整内容</a>
            {{if .UnsubscribeURL}}
            <a href="{{.UnsubscribeURL}}" target="_blank" style="color:rgb(156,163,175);text-decoration:none;position:absolute;right:0;font-size:12px;top:.75rem">退订</a>
            {{end}}
          </td></tr></tbody>
        </table>
        <hr style="width:100%;border:none;border-top:1px solid #eaeaea" />
        <p style="font-size:10px;line-height:24px;margin:16px 0;text-align:center;color:rgb(156,163,175)">本邮件为系统自动发送，请勿直接回复~<br />©{{year}} Copyright {{.OwnerName}}</p>
      </td></tr>
    </tbody>
  </table>
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
	Master       string
	IP           string
	Agent        string
	URL          string
	OwnerAvatar  string
	SiteName     string
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
	Master          string
	OwnerAvatar     string
	SiteName        string
}

// NewsletterData is the data for newsletter emails.
type NewsletterData struct {
	OwnerName      string
	OwnerAvatar    string
	Title          string
	Text           string
	DetailURL      string
	UnsubscribeURL string
	SiteName       string
}

func renderTemplate(tpl string, data interface{}) (string, error) {
	t, err := template.New("").Funcs(template.FuncMap{
		"year": func() int {
			return time.Now().Year()
		},
	}).Parse(tpl)
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
	if strings.TrimSpace(data.Master) == "" {
		data.Master = "Mx Space"
	}
	if strings.TrimSpace(data.OwnerAvatar) == "" {
		data.OwnerAvatar = "https://cdn.jsdelivr.net/gh/mx-space/.github@main/uwu.png"
	}
	if strings.TrimSpace(data.IP) == "" {
		data.IP = "-"
	}
	if strings.TrimSpace(data.Agent) == "" {
		data.Agent = "-"
	}
	if strings.TrimSpace(data.URL) == "" {
		data.URL = "-"
	}
	siteName := strings.TrimSpace(data.SiteName)
	if siteName == "" {
		siteName = "Mx Space"
	}
	html, err := renderTemplate(commentNotifyTpl, data)
	if err != nil {
		return err
	}
	return s.Send(Message{
		To:      []string{to},
		Subject: fmt.Sprintf("[%s] 有新回复了耶~", siteName),
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
	if strings.TrimSpace(data.Master) == "" {
		data.Master = "Mx Space"
	}
	if strings.TrimSpace(data.OwnerAvatar) == "" {
		data.OwnerAvatar = "https://cdn.jsdelivr.net/gh/mx-space/.github@main/uwu.png"
	}
	siteName := strings.TrimSpace(data.SiteName)
	if siteName == "" {
		siteName = "Mx Space"
	}
	html, err := renderTemplate(replyNotifyTpl, data)
	if err != nil {
		return err
	}
	return s.Send(Message{
		To:      []string{to},
		Subject: fmt.Sprintf("[%s] 主人给你了新的回复呐", siteName),
		HTML:    html,
	})
}

// SendNewsletter sends a newsletter email for newly published content.
func (s *Sender) SendNewsletter(to string, data NewsletterData) error {
	if strings.TrimSpace(data.OwnerName) == "" {
		data.OwnerName = "Mx Space"
	}
	if strings.TrimSpace(data.OwnerAvatar) == "" {
		data.OwnerAvatar = "https://cdn.jsdelivr.net/gh/mx-space/.github@main/uwu.png"
	}
	siteName := strings.TrimSpace(data.SiteName)
	if siteName == "" {
		siteName = "Mix Space"
	}
	html, err := renderTemplate(newsletterTpl, data)
	if err != nil {
		return err
	}
	return s.Send(Message{
		To:      []string{to},
		Subject: fmt.Sprintf("[%s] 发布了新内容~", siteName),
		HTML:    html,
	})
}
