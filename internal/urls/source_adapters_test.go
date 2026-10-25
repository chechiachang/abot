package urls

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestPublicSourceAdaptersUseBoundedFixtures(t *testing.T) {
	public := netip.MustParseAddr("93.184.216.34")
	resolver := mapResolver{}
	for _, host := range []string{
		"github.com", "api.github.com", "raw.githubusercontent.com", "docs.google.com",
		"reddit.com", "www.reddit.com", "truthsocial.com", "x.com", "threads.net", "www.threads.net", "www.ptt.cc", "news.example",
	} {
		resolver[host] = []netip.Addr{public}
	}
	readme := base64.StdEncoding.EncodeToString([]byte("# Project\n\nREADME fixture"))
	fixtures := []struct {
		name, target, source, contains string
		fetch                          func(string) response
	}{
		{"github readme", "https://github.com/acme/project", "github", "README fixture", func(target string) response {
			if !strings.HasPrefix(target, "https://api.github.com/repos/acme/project/readme") {
				return response{status: http.StatusNotFound}
			}
			return response{status: http.StatusOK, contentType: "application/json", body: []byte(`{"name":"README.md","encoding":"base64","content":"` + readme + `"}`)}
		}},
		{"github blob", "https://github.com/acme/project/blob/main/README.md", "github", "raw README", func(target string) response {
			if !strings.HasPrefix(target, "https://raw.githubusercontent.com/acme/project/main/README.md") {
				return response{status: http.StatusNotFound}
			}
			return response{status: http.StatusOK, contentType: "text/plain", body: []byte("raw README")}
		}},
		{"google docs", "https://docs.google.com/document/d/publicdoc/edit", "google_docs", "public notes", func(target string) response {
			if !strings.Contains(target, "/document/d/publicdoc/export") {
				return response{status: http.StatusNotFound}
			}
			return response{status: http.StatusOK, contentType: "text/plain", body: []byte("public notes")}
		}},
		{"reddit", "https://www.reddit.com/r/example/comments/abc/post", "reddit", "reddit post", func(target string) response {
			if !strings.Contains(target, "/comments/abc.json") {
				return response{status: http.StatusNotFound}
			}
			return response{status: http.StatusOK, contentType: "application/json", body: []byte(`[{"data":{"children":[{"data":{"title":"Reddit fixture","selftext":"reddit post"}}]}}]`)}
		}},
		{"truth social", "https://truthsocial.com/@user/12345", "truth_social", "truth post", func(target string) response {
			if !strings.Contains(target, "/api/v1/statuses/12345") {
				return response{status: http.StatusNotFound}
			}
			return response{status: http.StatusOK, contentType: "application/json", body: []byte(`{"content":"<p>truth post</p>"}`)}
		}},
		{"x", "https://x.com/user/status/10", "x", "x fixture", htmlFixture("x fixture")},
		{"threads", "https://www.threads.net/@user/post/10", "threads", "threads fixture", htmlFixture("threads fixture")},
		{"ptt", "https://www.ptt.cc/bbs/Gossiping/M.123.html", "ptt", "ptt fixture", htmlFixture("ptt fixture")},
		{"news", "https://news.example/article", "web", "news fixture", htmlFixture("news fixture")},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			reader := testReader(t, resolver)
			reader.fetch = func(_ context.Context, _, target string, _ http.Header, _ []byte) (response, error) {
				return fixture.fetch(target), nil
			}
			result, err := reader.Read(context.Background(), fixture.target)
			if err != nil || result.Source != fixture.source || !strings.Contains(result.Content, fixture.contains) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func htmlFixture(text string) func(string) response {
	return func(string) response {
		return response{status: http.StatusOK, contentType: "text/html", body: []byte("<html><head><title>Fixture</title></head><body><article><p>" + text + "</p></article></body></html>")}
	}
}
