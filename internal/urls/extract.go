package urls

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	xhtml "golang.org/x/net/html"
)

func (r *Reader) readSource(ctx context.Context, source *url.URL) (Result, error) {
	host := strings.ToLower(strings.TrimSuffix(source.Hostname(), "."))
	switch {
	case host == "youtu.be" || host == "youtube.com" || strings.HasSuffix(host, ".youtube.com"):
		return r.readYouTube(ctx, source)
	case host == "reddit.com" || strings.HasSuffix(host, ".reddit.com"):
		return r.readReddit(ctx, source)
	case host == "github.com" || host == "www.github.com":
		return r.readGitHub(ctx, source)
	case host == "docs.google.com":
		return r.readGoogleDoc(ctx, source)
	case host == "truthsocial.com" || host == "www.truthsocial.com":
		return r.readTruthSocial(ctx, source)
	default:
		resp, finalURL, err := r.fetchFollowingRedirects(ctx, http.MethodGet, source.String(), nil, nil)
		if err != nil {
			return Result{}, err
		}
		return r.extractResponse(ctx, source.String(), finalURL, sourceName(host), resp)
	}
}

func (r *Reader) extractResponse(ctx context.Context, sourceURL, finalURL, source string, resp response) (Result, error) {
	contentType := strings.ToLower(strings.Split(resp.contentType, ";")[0])
	pathURL, _ := url.Parse(finalURL)
	ext := strings.ToLower(filepath.Ext(pathURL.Path))
	if contentType == "application/pdf" || extIn(ext, ".pdf") {
		return r.extractDocument(ctx, sourceURL, finalURL, source, "download.pdf", resp.body)
	}
	if contentType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document" || extIn(ext, ".docx") {
		return r.extractDocument(ctx, sourceURL, finalURL, source, "download.docx", resp.body)
	}
	if contentType == "application/vnd.oasis.opendocument.text" || extIn(ext, ".odt") {
		return r.extractDocument(ctx, sourceURL, finalURL, source, "download.odt", resp.body)
	}
	if contentType == "application/rtf" || contentType == "text/rtf" || extIn(ext, ".rtf") {
		return r.extractDocument(ctx, sourceURL, finalURL, source, "download.rtf", resp.body)
	}
	switch {
	case strings.Contains(contentType, "html") || contentType == "application/xhtml+xml" || contentType == "":
		title, text, err := extractHTML(resp.body)
		if err != nil {
			return Result{}, &Error{Category: "extraction_failed", Message: "Page content could not be extracted."}
		}
		if text == "" {
			return Result{}, &Error{Category: "extraction_failed", Message: "Page contained no readable text."}
		}
		return Result{URL: sourceURL, FinalURL: finalURL, Source: source, Title: title, Content: truncate(text, r.config.MaxOutputBytes)}, nil
	case contentType == "text/plain" || strings.HasPrefix(contentType, "text/") || contentType == "application/xml":
		text := strings.TrimSpace(string(resp.body))
		if text == "" {
			return Result{}, &Error{Category: "extraction_failed", Message: "Source contained no readable text."}
		}
		return Result{URL: sourceURL, FinalURL: finalURL, Source: source, Content: truncate(text, r.config.MaxOutputBytes)}, nil
	case strings.Contains(contentType, "json"):
		title, text, err := extractJSON(resp.body)
		if err != nil || text == "" {
			return Result{}, &Error{Category: "extraction_failed", Message: "JSON source contained no readable text."}
		}
		return Result{URL: sourceURL, FinalURL: finalURL, Source: source, Title: title, Content: truncate(text, r.config.MaxOutputBytes)}, nil
	default:
		return Result{}, &Error{Category: "unsupported_source", Message: "This URL does not return a supported text, HTML, or document format."}
	}
}

func (r *Reader) extractDocument(ctx context.Context, sourceURL, finalURL, source, name string, data []byte) (Result, error) {
	if r.media == nil {
		return Result{}, &Error{Category: "unsupported_source", Message: "Local document conversion is not configured."}
	}
	item, err := r.media.Save(ctx, name, bytes.NewReader(data))
	if err != nil {
		var mediaErr interface{ Error() string }
		if errors.As(err, &mediaErr) {
			return Result{}, &Error{Category: "limit", Message: mediaErr.Error()}
		}
		return Result{}, &Error{Category: "extraction_failed", Message: "Document could not be stored for local conversion."}
	}
	defer os.Remove(item.Path)
	text, _, err := r.media.Extract(ctx, item)
	if err != nil {
		return Result{}, &Error{Category: "extraction_failed", Message: err.Error()}
	}
	if text == "" {
		return Result{}, &Error{Category: "extraction_failed", Message: "Document contained no extractable text."}
	}
	return Result{URL: sourceURL, FinalURL: finalURL, Source: source, Content: truncate(text, r.config.MaxOutputBytes)}, nil
}

func extractHTML(body []byte) (string, string, error) {
	document, err := xhtml.Parse(io.LimitReader(bytes.NewReader(body), int64(len(body))))
	if err != nil {
		return "", "", err
	}
	var title strings.Builder
	var content strings.Builder
	var walk func(*xhtml.Node, bool)
	walk = func(node *xhtml.Node, inTitle bool) {
		if node.Type == xhtml.ElementNode {
			switch strings.ToLower(node.Data) {
			case "script", "style", "noscript", "svg", "template", "iframe", "nav", "footer", "header", "aside":
				return
			case "title":
				inTitle = true
			case "h1", "h2", "h3", "h4", "p", "div", "li", "article", "section", "blockquote", "pre", "br", "tr":
				content.WriteString("\n\n")
			}
		}
		if node.Type == xhtml.TextNode {
			text := strings.TrimSpace(node.Data)
			if text != "" {
				if inTitle {
					title.WriteString(text)
					title.WriteByte(' ')
				} else {
					content.WriteString(text)
					content.WriteByte(' ')
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inTitle)
		}
	}
	walk(document, false)
	text := normalizeText(content.String())
	if text == "" {
		text = metaContent(document, "description")
	}
	return strings.TrimSpace(normalizeText(title.String())), text, nil
}

func metaContent(root *xhtml.Node, name string) string {
	var result string
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if result != "" || node.Type != xhtml.ElementNode {
			return
		}
		if strings.EqualFold(node.Data, "meta") {
			var property, content string
			for _, attr := range node.Attr {
				switch strings.ToLower(attr.Key) {
				case "name", "property":
					property = strings.ToLower(attr.Val)
				case "content":
					content = attr.Val
				}
			}
			if property == name || property == "og:"+name {
				result = strings.TrimSpace(content)
			}
		}
		for child := node.FirstChild; child != nil && result == ""; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return result
}

func normalizeText(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r", ""), "\n")
	var result []string
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if len(result) > 0 && result[len(result)-1] != "" {
				result = append(result, "")
			}
			continue
		}
		result = append(result, line)
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}

func extractJSON(body []byte) (string, string, error) {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return "", "", err
	}
	var title string
	var texts []string
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				item := typed[key]
				lower := strings.ToLower(key)
				if lower == "title" && title == "" {
					if text, ok := item.(string); ok {
						title = stripMarkup(text)
					}
				}
				if lower == "selftext" || lower == "body" || lower == "text" || lower == "content" || lower == "description" || lower == "markdown" || lower == "transcript" {
					if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
						texts = append(texts, stripMarkup(text))
					}
				}
				if lower != "selftext" && lower != "body" && lower != "text" && lower != "content" && lower != "description" && lower != "markdown" && lower != "transcript" {
					walk(item)
				}
			}
		case []any:
			for _, item := range typed {
				walk(item)
			}
		}
	}
	walk(value)
	return title, normalizeText(strings.Join(texts, "\n\n")), nil
}

func stripMarkup(value string) string {
	_, text, err := extractHTML([]byte(value))
	if err == nil && text != "" {
		return text
	}
	return strings.TrimSpace(value)
}

func sourceName(host string) string {
	switch {
	case host == "x.com" || host == "twitter.com" || host == "www.twitter.com":
		return "x"
	case host == "threads.net" || strings.HasSuffix(host, ".threads.net"):
		return "threads"
	case host == "ptt.cc" || strings.HasSuffix(host, ".ptt.cc"):
		return "ptt"
	default:
		return "web"
	}
}

func extIn(ext string, allowed ...string) bool {
	for _, candidate := range allowed {
		if ext == candidate {
			return true
		}
	}
	return false
}
