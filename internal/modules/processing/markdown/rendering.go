package markdown

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	htmlrenderer "github.com/yuin/goldmark/renderer/html"
)

//go:embed assets/markdown/markdown.css
var markdownBaseStyle string

//go:embed assets/markdown/theme/newsprint.css
var markdownThemeNewsprint string

//go:embed assets/markdown/theme/github.css
var markdownThemeGithub string

//go:embed assets/markdown/theme/han.css
var markdownThemeHan string

//go:embed assets/markdown/theme/gothic.css
var markdownThemeGothic string

type RenderedHTMLStructure struct {
	Body         []string `json:"body"`
	ExtraScripts []string `json:"extraScripts"`
	Script       []string `json:"script"`
	Link         []string `json:"link"`
	Style        []string `json:"style"`
}

type RenderDocumentOptions struct {
	Title  string
	Info   string
	Footer string
}

var markdownEngine = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.Table,
		extension.Strikethrough,
		extension.TaskList,
		extension.Linkify,
		extension.Typographer,
	),
	goldmark.WithRendererOptions(
		htmlrenderer.WithHardWraps(),
		htmlrenderer.WithXHTML(),
	),
)

var (
	spoilerPattern       = regexp.MustCompile(`\|\|([\s\S]+?)\|\|`)
	inlineKatexPattern   = regexp.MustCompile(`\$([^\$\n]+?)\$`)
	mentionPattern       = regexp.MustCompile(`\b(GH|TW|TG)@([A-Za-z0-9_]+)\b`)
	containerBlockRegex  = regexp.MustCompile(`(?ms)^\s*:::\s*(gallery|banner)\s*(?:\{(.*?)\})?\s*\n(.*?)\n\s*:::\s*(?:\n|$)`)
	mermaidCodeRegex     = regexp.MustCompile(`(?is)<pre><code class="language-mermaid">([\s\S]*?)</code></pre>`)
	imageTagRegex        = regexp.MustCompile(`(?is)<img\s+[^>]*>`)
	imageAttrRegex       = regexp.MustCompile(`([a-zA-Z:_-]+)\s*=\s*"([^"]*)"`)
	figureParagraphRegex = regexp.MustCompile(`(?is)<p>\s*(<figure>[\s\S]*?</figure>)\s*</p>`)
)

func RenderMarkdownContent(markdownText string) string {
	text := strings.TrimSpace(markdownText)
	if text == "" {
		return ""
	}

	text = replaceContainerBlocks(text)
	text = replaceMention(text)
	text = replaceSpoiler(text)
	text = replaceInlineKatex(text)

	var out bytes.Buffer
	if err := markdownEngine.Convert([]byte(text), &out); err != nil {
		return template.HTMLEscapeString(text)
	}

	html := out.String()
	html = rewriteCodeBlocks(html)
	html = rewriteImages(html)
	return html
}

func BuildRenderedMarkdownHTMLStructure(html, title, theme string) RenderedHTMLStructure {
	return RenderedHTMLStructure{
		Body: []string{
			fmt.Sprintf("<article><h1>%s</h1>%s</article>", template.HTMLEscapeString(title), html),
		},
		ExtraScripts: []string{
			`<script src="https://lf26-cdn-tos.bytecdntp.com/cdn/expire-1-M/mermaid/8.9.0/mermaid.min.js"></script>`,
			`<script src="https://lf26-cdn-tos.bytecdntp.com/cdn/expire-1-M/prism/1.23.0/components/prism-core.min.js"></script>`,
			`<script src="https://lf26-cdn-tos.bytecdntp.com/cdn/expire-1-M/prism/1.23.0/plugins/autoloader/prism-autoloader.min.js"></script>`,
			`<script src="https://lf3-cdn-tos.bytecdntp.com/cdn/expire-1-M/prism/1.23.0/plugins/line-numbers/prism-line-numbers.min.js"></script>`,
			`<script src="https://lf6-cdn-tos.bytecdntp.com/cdn/expire-1-M/KaTeX/0.15.2/katex.min.js" async defer></script>`,
		},
		Script: []string{
			`window.mermaid.initialize({theme: 'default',startOnLoad: false})`,
			`window.mermaid.init(undefined, '.mermaid')`,
			`window.onload = () => { document.querySelectorAll('.katex-render').forEach(el => { window.katex.render(el.innerHTML, el, { throwOnError: false }) }) }`,
		},
		Link: []string{
			`<link href="https://cdn.jsdelivr.net/gh/PrismJS/prism-themes@master/themes/prism-one-light.css" rel="stylesheet" />`,
			`<link href="https://lf26-cdn-tos.bytecdntp.com/cdn/expire-1-M/prism/1.23.0/plugins/line-numbers/prism-line-numbers.min.css" rel="stylesheet" />`,
			`<link href="https://lf9-cdn-tos.bytecdntp.com/cdn/expire-1-M/KaTeX/0.15.2/katex.min.css" rel="stylesheet" />`,
		},
		Style: []string{
			markdownBaseStyle,
			resolveThemeStyle(theme),
		},
	}
}

func RenderMarkdownHTMLDocument(structure RenderedHTMLStructure, options RenderDocumentOptions) string {
	var b strings.Builder
	b.Grow(4096)

	title := template.HTMLEscapeString(strings.TrimSpace(options.Title))
	if title == "" {
		title = "Markdown"
	}

	b.WriteString("<!DOCTYPE html>\n<html lang=\"zh-cn\">\n")
	b.WriteString("  <head>\n")
	b.WriteString("    <meta charset=\"UTF-8\" />\n")
	b.WriteString("    <meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\" />\n")
	b.WriteString("    <meta http-equiv=\"X-UA-Compatible\" content=\"ie=edge\" />\n")
	b.WriteString("    <meta name=\"referrer\" content=\"no-referrer\" />\n")
	b.WriteString("    <style>\n")
	b.WriteString(strings.Join(structure.Style, "\n"))
	b.WriteString("\n    </style>\n")
	b.WriteString("    ")
	b.WriteString(strings.Join(structure.Link, "\n    "))
	b.WriteString("\n")
	b.WriteString("    <style>\n")
	b.WriteString("      code[class*='language-'],\n")
	b.WriteString("      pre[class*='language-'] {\n")
	b.WriteString("        font-size: 14px;\n")
	b.WriteString("      }\n")
	b.WriteString("    </style>\n")
	b.WriteString("    <title>")
	b.WriteString(title)
	b.WriteString("</title>\n")
	b.WriteString("  </head>\n\n")
	b.WriteString("  <body class=\"markdown-body line-numbers\" id=\"write\">\n")
	b.WriteString("    <p style=\"margin: auto; margin: 20px; text-align: center; opacity: 0.8;\">\n")
	b.WriteString("      ")
	b.WriteString(strings.TrimSpace(options.Info))
	b.WriteString("\n")
	b.WriteString("    </p>\n")
	b.WriteString("    ")
	b.WriteString(strings.Join(structure.Body, "\n    "))
	b.WriteString("\n")
	b.WriteString("  </body>\n\n")

	if footer := strings.TrimSpace(options.Footer); footer != "" {
		b.WriteString("  <footer style=\"text-align: right; padding: 2em 0; font-size: 0.8em; line-height: 2;\">\n")
		b.WriteString("    ")
		b.WriteString(footer)
		b.WriteString("\n")
		b.WriteString("  </footer>\n")
	}

	b.WriteString("  ")
	b.WriteString(strings.Join(structure.ExtraScripts, "\n  "))
	b.WriteString("\n")
	b.WriteString("  <script>\n")
	b.WriteString("    ")
	b.WriteString(strings.Join(structure.Script, "\n    "))
	b.WriteString("\n")
	b.WriteString("  </script>\n")
	b.WriteString("</html>")

	return b.String()
}

func resolveThemeStyle(theme string) string {
	switch strings.ToLower(strings.TrimSpace(theme)) {
	case "github":
		return markdownThemeGithub
	case "han":
		return markdownThemeHan
	case "gothic":
		return markdownThemeGothic
	default:
		return markdownThemeNewsprint
	}
}

func replaceSpoiler(text string) string {
	return spoilerPattern.ReplaceAllStringFunc(text, func(raw string) string {
		match := spoilerPattern.FindStringSubmatch(raw)
		if len(match) < 2 {
			return raw
		}
		content := template.HTMLEscapeString(strings.TrimSpace(match[1]))
		return `<span class="spoiler" style="filter: invert(25%)">` + content + `</span>`
	})
}

func replaceInlineKatex(text string) string {
	return inlineKatexPattern.ReplaceAllStringFunc(text, func(raw string) string {
		match := inlineKatexPattern.FindStringSubmatch(raw)
		if len(match) < 2 {
			return raw
		}
		content := template.HTMLEscapeString(strings.TrimSpace(match[1]))
		return `<span class="katex-render">` + content + `</span>`
	})
}

func replaceMention(text string) string {
	return mentionPattern.ReplaceAllStringFunc(text, func(raw string) string {
		match := mentionPattern.FindStringSubmatch(raw)
		if len(match) < 3 {
			return raw
		}
		prefix := match[1]
		username := match[2]
		base := map[string]string{
			"GH": "https://github.com/",
			"TW": "https://twitter.com/",
			"TG": "https://t.me/",
		}[prefix]
		if base == "" {
			return raw
		}
		name := template.HTMLEscapeString(username)
		return fmt.Sprintf(`<a target="_blank" class="mention" rel="noreferrer nofollow" href="%s%s">%s</a>`, base, name, name)
	})
}

func replaceContainerBlocks(text string) string {
	return containerBlockRegex.ReplaceAllStringFunc(text, func(raw string) string {
		match := containerBlockRegex.FindStringSubmatch(raw)
		if len(match) < 4 {
			return raw
		}

		name := strings.ToLower(strings.TrimSpace(match[1]))
		params := normalizeClassName(match[2])
		content := strings.TrimSpace(match[3])
		contentHTML := renderMarkdownFragment(content)

		switch name {
		case "gallery":
			return `<div class="container">` + contentHTML + `</div>`
		case "banner":
			className := "container banner"
			if params != "" {
				className += " " + params
			}
			return `<div class="` + className + `">` + contentHTML + `</div>`
		default:
			return raw
		}
	})
}

func normalizeClassName(raw string) string {
	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) == 0 {
		return ""
	}

	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		valid := true
		for _, ch := range part {
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
				continue
			}
			valid = false
			break
		}
		if valid {
			out = append(out, part)
		}
	}
	return strings.Join(out, " ")
}

func renderMarkdownFragment(content string) string {
	var out bytes.Buffer
	if err := markdownEngine.Convert([]byte(content), &out); err != nil {
		return template.HTMLEscapeString(content)
	}
	return out.String()
}

func rewriteCodeBlocks(html string) string {
	return mermaidCodeRegex.ReplaceAllString(html, `<pre class="mermaid">$1</pre>`)
}

func rewriteImages(html string) string {
	processed := imageTagRegex.ReplaceAllStringFunc(html, func(tag string) string {
		attrs := parseImageAttrs(tag)
		src := strings.TrimSpace(attrs["src"])
		if src == "" {
			return tag
		}

		alt := strings.TrimSpace(attrs["alt"])
		title := strings.TrimSpace(attrs["title"])
		escapedSrc := template.HTMLEscapeString(src)

		if strings.HasPrefix(alt, "!") || strings.HasPrefix(alt, "¡") {
			caption := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(alt, "!"), "¡"))
			if caption == "" {
				caption = title
			}
			caption = template.HTMLEscapeString(caption)
			return `<figure><img src="` + escapedSrc + `"/><figcaption style="text-align: center; margin: 1em auto;">` + caption + `</figcaption></figure>`
		}

		return `<img src="` + escapedSrc + `"/>`
	})
	return figureParagraphRegex.ReplaceAllString(processed, "$1")
}

func parseImageAttrs(tag string) map[string]string {
	attrs := make(map[string]string)
	matches := imageAttrRegex.FindAllStringSubmatch(tag, -1)
	for _, item := range matches {
		if len(item) < 3 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(item[1]))
		if key == "" {
			continue
		}
		attrs[key] = item[2]
	}
	return attrs
}
