package telegram

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	LongOutputThreshold = 1000
	TelegramTextLimit   = 4096
)

type Publisher interface {
	Publish(context.Context, string, string) (string, error)
}

func PrepareAnswer(ctx context.Context, text string, publisher Publisher) (string, string, error) {
	if utf8.RuneCountInString(text) > LongOutputThreshold {
		if publisher == nil {
			return "", "", fmt.Errorf("response exceeds %d characters; configure MORSEL_API_KEY and MORSEL_PUBLISH_URL to publish long output", LongOutputThreshold)
		}
		link, err := publisher.Publish(ctx, "abot response", text)
		if err != nil {
			return "", "", fmt.Errorf("publish long response to Morsel: %w", err)
		}
		target, err := url.Parse(link)
		if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil {
			return "", "", fmt.Errorf("Morsel returned an invalid public HTTPS URL")
		}
		return `<a href="` + html.EscapeString(target.String()) + `">Open the full response</a>`, "HTML", nil
	}
	if utf8.RuneCountInString(text) > TelegramTextLimit {
		return "", "", fmt.Errorf("response exceeds Telegram's %d-character message limit", TelegramTextLimit)
	}
	return RenderHTML(text), "HTML", nil
}

func RenderHTML(text string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var out strings.Builder
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], "```") {
			if end := strings.Index(text[i+3:], "```"); end >= 0 {
				code := text[i+3 : i+3+end]
				if newline := strings.IndexByte(code, '\n'); newline >= 0 && !strings.Contains(code[:newline], " ") {
					code = code[newline+1:]
				}
				out.WriteString("<pre><code>")
				out.WriteString(html.EscapeString(strings.Trim(code, "\n")))
				out.WriteString("</code></pre>")
				i += 3 + end + 3
				continue
			}
		}
		if text[i] == '`' {
			if end := strings.IndexByte(text[i+1:], '`'); end >= 0 {
				out.WriteString("<code>")
				out.WriteString(html.EscapeString(text[i+1 : i+1+end]))
				out.WriteString("</code>")
				i += end + 2
				continue
			}
		}
		if i+2 <= len(text) && (strings.HasPrefix(text[i:], "**") || strings.HasPrefix(text[i:], "__")) {
			marker := text[i : i+2]
			if end := strings.Index(text[i+2:], marker); end > 0 {
				out.WriteString("<b>")
				out.WriteString(html.EscapeString(text[i+2 : i+2+end]))
				out.WriteString("</b>")
				i += end + 4
				continue
			}
		}
		if text[i] == '*' || text[i] == '_' {
			marker := text[i : i+1]
			if end := strings.Index(text[i+1:], marker); end > 0 {
				out.WriteString("<i>")
				out.WriteString(html.EscapeString(text[i+1 : i+1+end]))
				out.WriteString("</i>")
				i += end + 2
				continue
			}
		}
		if text[i] == '[' {
			if labelEnd := strings.IndexByte(text[i+1:], ']'); labelEnd >= 0 {
				labelEnd += i + 1
				if labelEnd+1 < len(text) && text[labelEnd+1] == '(' {
					urlEnd := strings.IndexByte(text[labelEnd+2:], ')')
					if urlEnd >= 0 {
						urlEnd += labelEnd + 2
						target := text[labelEnd+2 : urlEnd]
						if safeLink(target) {
							out.WriteString(`<a href="`)
							out.WriteString(html.EscapeString(target))
							out.WriteString(`">`)
							out.WriteString(html.EscapeString(text[i+1 : labelEnd]))
							out.WriteString("</a>")
							i = urlEnd + 1
							continue
						}
					}
				}
			}
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		if size == 0 {
			break
		}
		out.WriteString(html.EscapeString(text[i : i+size]))
		i += size
	}
	return out.String()
}

func safeLink(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil
}
