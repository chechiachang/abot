package polymarket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type Market struct {
	Question  string          `json:"question"`
	Slug      string          `json:"slug"`
	Volume    json.RawMessage `json:"volume"`
	VolumeNum json.RawMessage `json:"volumeNum"`
	EndDate   string          `json:"endDate"`
	Active    bool            `json:"active"`
	Closed    bool            `json:"closed"`
}

func (m Market) URL() string { return "https://polymarket.com/event/" + url.PathEscape(m.Slug) }

type Client struct {
	HTTP             *http.Client
	APIURL           string
	WSURL            string
	Timeout          time.Duration
	MaxResponseBytes int64
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		APIURL: "https://gamma-api.polymarket.com", WSURL: "wss://ws-subscriptions-clob.polymarket.com/ws/market",
		Timeout: 12 * time.Second, MaxResponseBytes: 2 << 20}
}

func (c *Client) Search(ctx context.Context, query string, limit int) ([]Market, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 100 || strings.ContainsAny(query, "\r\n\x00") {
		return nil, errors.New("provide a market search of 1 to 100 characters")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		return nil, errors.New("market result limit cannot exceed 10")
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	target := strings.TrimRight(c.APIURL, "/") + "/markets?active=true&closed=false&limit=100&order=volume&ascending=false"
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
	if err != nil {
		return nil, errors.New("could not prepare public market query")
	}
	req.Header.Set("User-Agent", "abot/1 polymarket-reader")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if requestCtx.Err() != nil {
			return nil, requestCtx.Err()
		}
		return nil, errors.New("Polymarket public market request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Polymarket returned HTTP %d", resp.StatusCode)
	}
	maxBytes := c.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = 2 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil || int64(len(body)) > maxBytes {
		return nil, errors.New("Polymarket response exceeded its size limit")
	}
	var markets []Market
	if err := json.Unmarshal(body, &markets); err != nil {
		return nil, errors.New("Polymarket returned invalid market data")
	}
	lower := strings.ToLower(query)
	results := make([]Market, 0, limit)
	for _, market := range markets {
		if !market.Active || market.Closed || market.Slug == "" || market.Question == "" {
			continue
		}
		if strings.Contains(strings.ToLower(market.Question), lower) || strings.Contains(strings.ToLower(market.Slug), lower) {
			results = append(results, market)
			if len(results) == limit {
				break
			}
		}
	}
	return results, nil
}

func (c *Client) Watch(ctx context.Context, assetIDs []string, onEvent func(string)) error {
	if c == nil || len(assetIDs) == 0 || len(assetIDs) > 50 {
		return errors.New("subscribe to between 1 and 50 Polymarket asset IDs")
	}
	seen := map[string]bool{}
	for _, id := range assetIDs {
		if !regexp.MustCompile("^[0-9]{1,80}$").MatchString(id) || seen[id] {
			return errors.New("Polymarket asset IDs must be unique decimal IDs")
		}
		seen[id] = true
	}
	if onEvent == nil {
		return errors.New("Polymarket subscription requires an event handler")
	}
	backoff := time.Second
	for ctx.Err() == nil {
		dialer := websocket.Dialer{HandshakeTimeout: c.timeout(), Proxy: nil}
		conn, _, err := dialer.DialContext(ctx, c.WSURL, nil)
		if err == nil {
			err = c.watchConnection(ctx, conn, assetIDs, onEvent)
		}
		if ctx.Err() != nil {
			return nil
		}
		if !wait(ctx, backoff) {
			return nil
		}
		backoff = min(backoff*2, 15*time.Second)
		if err == nil {
			backoff = time.Second
		}
	}
	return nil
}

func (c *Client) watchConnection(ctx context.Context, conn *websocket.Conn, assetIDs []string, onEvent func(string)) error {
	defer conn.Close()
	conn.SetReadLimit(1 << 20)
	if err := conn.WriteJSON(map[string]any{"assets_ids": assetIDs, "type": "market"}); err != nil {
		return err
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	defer close(stop)
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if len(message) > 0 {
			onEvent(string(message))
		}
	}
}

func (c *Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 12 * time.Second
}

func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
