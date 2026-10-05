package mail

import (
	"os"
	"strings"
	"testing"
)

func TestDefaultTemplatesRenderWithProps(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"owner": ownerTemplateProps(CommentNotifyData{
			Title: "T", Content: "<script>x</script>", Author: "A", IP: "1.2.3.4", ArticleURL: "https://example.com/p",
		}),
		"guest":      guestTemplateProps(ReplyNotifyData{Title: "T", ReplyContent: "R", OriginalContent: "O", Master: "M"}),
		"newsletter": newsletterTemplateProps(NewsletterData{Title: "T", Text: "body", OwnerName: "M", DetailURL: "https://example.com"}),
	}
	for kind, props := range cases {
		tpl, err := os.ReadFile("../../modules/system/core/configs/email-template/" + kind + ".template.ejs")
		if err != nil {
			t.Fatal(err)
		}
		html, err := RenderEJS(string(tpl), props)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if !strings.Contains(html, "T") || strings.Contains(html, "<script>x") {
			t.Fatalf("%s: unexpected output", kind)
		}
	}
}

func TestRenderEJSReportsTemplateErrors(t *testing.T) {
	if _, err := RenderEJS("<%= missing.field %>", map[string]interface{}{}); err == nil {
		t.Fatal("expected an error for an undefined variable")
	}
}
