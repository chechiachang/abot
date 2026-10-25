package urls

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chechiachang/abot/internal/media"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type Config struct {
	Timeout                   time.Duration
	MaxBytes                  int64
	MaxOutputBytes            int
	MaxRedirects              int
	MaxAudioDuration          time.Duration
	FirecrawlEndpoint         string
	FirecrawlAPIKey           string
	FirecrawlExternalTransfer bool
	BrowserExecutable         string
	YouTubeDL                 string
}

type Result struct {
	URL      string `json:"url"`
	FinalURL string `json:"final_url,omitempty"`
	Source   string `json:"source"`
	Title    string `json:"title,omitempty"`
	Content  string `json:"content"`
}

type Error struct {
	Category string `json:"category"`
	Message  string `json:"message"`
}

func (e *Error) Error() string { return e.Category + ": " + e.Message }

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type response struct {
	status      int
	contentType string
	location    string
	body        []byte
}

type Reader struct {
	config   Config
	resolver Resolver
	fetch    func(context.Context, string, string, http.Header, []byte) (response, error)
	media    *media.Store
}

func NewReader(cfg Config, processor *media.Store) (*Reader, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 10 << 20
	}
	if cfg.MaxOutputBytes == 0 {
		cfg.MaxOutputBytes = 12 << 10
	}
	if cfg.MaxRedirects == 0 {
		cfg.MaxRedirects = 5
	}
	if cfg.MaxAudioDuration == 0 {
		cfg.MaxAudioDuration = 5 * time.Minute
	}
	if cfg.Timeout <= 0 || cfg.Timeout > 10*time.Minute || cfg.MaxBytes < 1024 || cfg.MaxBytes > 50<<20 || cfg.MaxOutputBytes < 1024 || cfg.MaxOutputBytes > 64<<10 || cfg.MaxRedirects < 0 || cfg.MaxRedirects > 10 || cfg.MaxAudioDuration <= 0 || cfg.MaxAudioDuration > time.Hour {
		return nil, errors.New("URL reader limits are outside their supported range")
	}
	r := &Reader{config: cfg, resolver: net.DefaultResolver, media: processor}
	r.fetch = r.fetchHTTP
	return r, nil
}

func (r *Reader) Read(ctx context.Context, raw string) (Result, error) {
	ctx, span := otel.Tracer("abot/urls").Start(ctx, "url.read")
	defer span.End()
	if target, err := url.Parse(raw); err == nil {
		span.SetAttributes(attribute.String("url.scheme", target.Scheme), attribute.String("server.address", target.Hostname()))
	}
	result, err := r.read(ctx, raw)
	if err != nil {
		span.SetStatus(codes.Error, "URL extraction failed")
		span.SetAttributes(attribute.String("error.category", sourceErrorCategory(err)))
	} else {
		span.SetAttributes(attribute.String("source.kind", result.Source), attribute.Int("content.bytes", len(result.Content)))
	}
	return result, err
}

func (r *Reader) read(ctx context.Context, raw string) (Result, error) {
	if r == nil {
		return Result{}, &Error{Category: "extraction_failed", Message: "URL reader is not configured"}
	}
	ctx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()
	u, _, err := r.validateURL(ctx, raw)
	if err != nil {
		return Result{}, err
	}
	canonicalURL := u.String()
	result, err := r.readSource(ctx, u)
	if err != nil && r.config.BrowserExecutable != "" && retryWithBrowser(err) && ctx.Err() == nil {
		if fallback, fallbackErr := r.readBrowser(ctx, canonicalURL); fallbackErr == nil {
			result, err = fallback, nil
		} else {
			err = fallbackErr
		}
	}
	if err != nil && r.config.FirecrawlExternalTransfer && r.config.FirecrawlAPIKey != "" && retryWithFirecrawl(err) && ctx.Err() == nil {
		if fallback, fallbackErr := r.readFirecrawl(ctx, canonicalURL); fallbackErr == nil {
			return fallback, nil
		} else {
			err = fallbackErr
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, contextError(ctx.Err())
		}
		return Result{}, err
	}
	result.URL = canonicalURL
	return result, nil
}

func (r *Reader) fetchFollowingRedirects(ctx context.Context, method, raw string, headers http.Header, body []byte) (response, string, error) {
	current := raw
	for redirects := 0; ; redirects++ {
		if _, _, err := r.validateURL(ctx, current); err != nil {
			return response{}, "", err
		}
		resp, err := r.fetch(ctx, method, current, headers, body)
		if err != nil {
			if ctx.Err() != nil {
				return response{}, "", contextError(ctx.Err())
			}
			var sourceErr *Error
			if errors.As(err, &sourceErr) {
				return response{}, "", sourceErr
			}
			return response{}, "", &Error{Category: "extraction_failed", Message: "Source request failed."}
		}
		if resp.status < 300 || resp.status > 399 {
			if resp.status < 200 || resp.status >= 300 {
				return response{}, "", &Error{Category: "http_status", Message: fmt.Sprintf("Source returned HTTP %d.", resp.status)}
			}
			return resp, current, nil
		}
		if method != http.MethodGet {
			return response{}, "", &Error{Category: "http_status", Message: "Source endpoint redirected a protected request."}
		}
		if redirects >= r.config.MaxRedirects {
			return response{}, "", &Error{Category: "limit", Message: "Source exceeded the redirect limit."}
		}
		location, err := url.Parse(resp.location)
		if err != nil || resp.location == "" {
			return response{}, "", &Error{Category: "extraction_failed", Message: "Source returned an invalid redirect."}
		}
		base, _ := url.Parse(current)
		current = base.ResolveReference(location).String()
		if method != http.MethodGet {
			method, body = http.MethodGet, nil
		}
	}
}

func (r *Reader) validateURL(ctx context.Context, raw string) (*url.URL, []netip.Addr, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return nil, nil, &Error{Category: "unsupported_source", Message: "A complete HTTP(S) URL is required."}
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, nil, &Error{Category: "unsupported_source", Message: "Only public HTTP(S) URLs without credentials are supported."}
	}
	u.Fragment = ""
	u.RawFragment = ""
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, nil, &Error{Category: "unsupported_source", Message: "The URL contains an invalid port."}
		}
	}
	ips, err := r.lookupPublic(ctx, u.Hostname())
	if err != nil {
		return nil, nil, err
	}
	return u, ips, nil
}

func (r *Reader) lookupPublic(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		if !publicIP(ip) {
			return nil, &Error{Category: "private_target", Message: "Private, loopback, link-local, and reserved network targets are blocked."}
		}
		return []netip.Addr{ip.Unmap()}, nil
	}
	ips, err := r.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, &Error{Category: "dns_failure", Message: "The URL hostname could not be resolved."}
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, &Error{Category: "private_target", Message: "A URL hostname resolved to a private or reserved network target."}
		}
	}
	return ips, nil
}

func publicIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() || ip.Zone() != "" {
		return false
	}
	if ip.Is4() {
		for _, prefix := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4"} {
			if netip.MustParsePrefix(prefix).Contains(ip) {
				return false
			}
		}
		return true
	}
	global := netip.MustParsePrefix("2000::/3")
	for _, prefix := range []string{"2001:db8::/32", "2001::/23", "2002::/16", "3fff::/20"} {
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return global.Contains(ip)
}

func (r *Reader) fetchHTTP(ctx context.Context, method, raw string, headers http.Header, body []byte) (response, error) {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := r.lookupPublic(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
			for _, ip := range ips {
				connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return connection, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       10 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: r.config.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, method, raw, bytes.NewReader(body))
	if err != nil {
		return response{}, err
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if len(body) > 0 && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(request)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	if resp.ContentLength > r.config.MaxBytes {
		return response{}, &Error{Category: "too_large", Message: "Source exceeds the configured download limit."}
	}
	limit := r.config.MaxBytes
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "firecrawl") {
		limit = min(limit, 2<<20)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return response{}, err
	}
	if int64(len(data)) > limit {
		return response{}, &Error{Category: "too_large", Message: "Source exceeds the configured download limit."}
	}
	return response{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), location: resp.Header.Get("Location"), body: data}, nil
}

func (r *Reader) readFirecrawl(ctx context.Context, target string) (Result, error) {
	if _, _, err := r.validateURL(ctx, r.config.FirecrawlEndpoint); err != nil {
		return Result{}, &Error{Category: "extraction_failed", Message: "Configured Firecrawl endpoint is unavailable."}
	}
	body, _ := json.Marshal(map[string]any{"url": target, "formats": []string{"markdown"}})
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+r.config.FirecrawlAPIKey)
	headers.Set("Content-Type", "application/json")
	resp, finalURL, err := r.fetchFollowingRedirects(ctx, http.MethodPost, r.config.FirecrawlEndpoint, headers, body)
	if err != nil {
		return Result{}, &Error{Category: "extraction_failed", Message: "Firecrawl fallback failed."}
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Markdown string `json:"markdown"`
			Metadata struct {
				Title string `json:"title"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.body, &payload); err != nil || !payload.Success || payload.Data.Markdown == "" {
		return Result{}, &Error{Category: "extraction_failed", Message: "Firecrawl returned no Markdown content."}
	}
	content := truncate(payload.Data.Markdown, r.config.MaxOutputBytes)
	return Result{URL: target, FinalURL: finalURL, Source: "firecrawl", Title: payload.Data.Metadata.Title, Content: content}, nil
}

func retryWithFirecrawl(err error) bool {
	var sourceErr *Error
	if !errors.As(err, &sourceErr) {
		return false
	}
	return sourceErr.Category == "http_status" || sourceErr.Category == "extraction_failed" || sourceErr.Category == "unsupported_source"
}

func contextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Category: "timeout", Message: "URL reading timed out."}
	}
	return &Error{Category: "cancelled", Message: "URL reading was cancelled."}
}

func truncate(value string, maxBytes int) string {
	value = strings.ToValidUTF8(value, "")
	if len(value) <= maxBytes {
		return value
	}
	suffix := "\n[truncated]"
	if maxBytes <= len(suffix) {
		value = value[:maxBytes]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
		return value
	}
	limit := maxBytes - len(suffix)
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + suffix
}
