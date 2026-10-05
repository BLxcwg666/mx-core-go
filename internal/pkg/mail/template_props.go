package mail

import "time"

// The props below follow the ones the original core passed to its EJS templates
// (and that the admin panel shows in the template editor preview).

func formatTemplateTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.Format("2006/1/2")
}

func isoOrNil(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339)
}

func ownerTemplateProps(d CommentNotifyData) map[string]interface{} {
	created := d.Created
	if created.IsZero() {
		created = time.Now()
	}
	return map[string]interface{}{
		"author":  d.Author,
		"avatar":  d.Avatar,
		"mail":    d.Mail,
		"text":    d.Content,
		"ip":      d.IP,
		"agent":   d.Agent,
		"created": created.Format(time.RFC3339),
		"url":     d.URL,
		"link":    d.ArticleURL,
		"time":    formatTemplateTime(created),
		"title":   d.Title,
		"master":  d.Master,
		"aggregate": map[string]interface{}{
			"post": map[string]interface{}{
				"title":    d.Title,
				"id":       d.RefID,
				"text":     d.RefText,
				"created":  isoOrNil(d.RefCreated),
				"modified": nil,
			},
			"commentor": map[string]interface{}{
				"author":     d.Author,
				"avatar":     d.Avatar,
				"mail":       d.Mail,
				"text":       d.Content,
				"ip":         d.IP,
				"agent":      d.Agent,
				"created":    created.Format(time.RFC3339),
				"isWhispers": d.IsWhispers,
				"location":   d.Location,
				"url":        d.URL,
			},
			"parent": map[string]interface{}{"text": d.ReplyContent},
			"owner": map[string]interface{}{
				"name":   d.Master,
				"avatar": d.OwnerAvatar,
				"mail":   d.OwnerMail,
				"url":    d.OwnerURL,
			},
		},
	}
}

func guestTemplateProps(d ReplyNotifyData) map[string]interface{} {
	return map[string]interface{}{
		"author": d.Master,
		"mail":   d.Mail,
		"text":   d.ReplyContent,
		"ip":     d.IP,
		"link":   d.ArticleURL,
		"time":   formatTemplateTime(d.Created),
		"title":  d.Title,
		"master": d.Master,
		"aggregate": map[string]interface{}{
			"parent": map[string]interface{}{"text": d.OriginalContent},
			"owner": map[string]interface{}{
				"name":   d.Master,
				"avatar": d.OwnerAvatar,
				"mail":   d.OwnerMail,
				"url":    d.OwnerURL,
			},
		},
	}
}

func newsletterTemplateProps(d NewsletterData) map[string]interface{} {
	return map[string]interface{}{
		"text":             d.Text,
		"title":            d.Title,
		"author":           d.OwnerName,
		"detail_link":      d.DetailURL,
		"unsubscribe_link": d.UnsubscribeURL,
		"master":           d.OwnerName,
		"aggregate": map[string]interface{}{
			"owner": map[string]interface{}{
				"name":   d.OwnerName,
				"avatar": d.OwnerAvatar,
			},
			"subscriber": map[string]interface{}{
				"email":     d.SubscriberEmail,
				"subscribe": d.SubscriberBitmask,
			},
			"post": map[string]interface{}{
				"text":    d.Text,
				"title":   d.Title,
				"id":      d.RefID,
				"created": isoOrNil(d.Created),
			},
		},
	}
}
