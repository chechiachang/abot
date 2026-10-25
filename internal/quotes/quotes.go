package quotes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	xhtml "golang.org/x/net/html"
)

type Quote struct {
	Symbol    string    `json:"symbol"`
	Name      string    `json:"name,omitempty"`
	Price     string    `json:"price,omitempty"`
	Currency  string    `json:"currency,omitempty"`
	CashBuy   string    `json:"cash_buy,omitempty"`
	CashSell  string    `json:"cash_sell,omitempty"`
	SpotBuy   string    `json:"spot_buy,omitempty"`
	SpotSell  string    `json:"spot_sell,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	Stale     bool      `json:"stale"`
}

type Error struct{ Category, Message string }

func (e *Error) Error() string { return e.Category + ": " + e.Message }

type cacheEntry struct {
	quote Quote
	until time.Time
}

type Client struct {
	http                             *http.Client
	timeout, cacheTTL                time.Duration
	maxBytes                         int64
	yahooURL, maxURL, fxURL, bankURL string
	mu                               sync.Mutex
	cache                            map[string]cacheEntry
}

func NewClient() *Client {
	return NewClientWithHTTP(&http.Client{Timeout: 8 * time.Second}, 8*time.Second, 24*time.Hour, 1<<20)
}
func NewClientWithHTTP(client *http.Client, timeout, cacheTTL time.Duration, maxBytes int64) *Client {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	if cacheTTL <= 0 {
		cacheTTL = 24 * time.Hour
	}
	if maxBytes < 1024 {
		maxBytes = 1 << 20
	}
	return &Client{http: client, timeout: timeout, cacheTTL: cacheTTL, maxBytes: maxBytes,
		yahooURL: "https://query1.finance.yahoo.com/v8/finance/chart/", maxURL: "https://max-api.maicoin.com/api/v2/tickers/",
		fxURL: "https://api.frankfurter.dev/v1/latest", bankURL: "https://rate.bot.com.tw/xrt?Lang=en-US", cache: map[string]cacheEntry{}}
}

func (c *Client) Read(ctx context.Context, raw string) (Quote, error) {
	if c == nil || c.http == nil {
		return Quote{}, &Error{"unavailable", "Quote client is not configured."}
	}
	symbol := strings.ToUpper(strings.TrimSpace(raw))
	if symbol == "" || len(symbol) > 40 {
		return Quote{}, &Error{"invalid_symbol", "Provide a supported ticker or currency pair."}
	}
	key, err := normalizeKey(symbol)
	if err != nil {
		return Quote{}, err
	}
	c.mu.Lock()
	if entry, ok := c.cache[key]; ok && time.Now().Before(entry.until) {
		c.mu.Unlock()
		entry.quote.Stale = entry.quote.Stale || stale(entry.quote.Timestamp, c.staleAfter(key))
		return entry.quote, nil
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, c.timeoutFor(key))
	defer cancel()
	var q Quote
	switch {
	case strings.HasPrefix(symbol, "MAX:"):
		q, err = c.readMAX(ctx, key, strings.TrimPrefix(symbol, "MAX:"))
	case strings.HasPrefix(symbol, "BOT:"):
		q, err = c.readBank(ctx, key, strings.TrimPrefix(symbol, "BOT:"))
	case strings.HasPrefix(symbol, "FX:"):
		q, err = c.readFX(ctx, key, strings.TrimPrefix(symbol, "FX:"))
	case strings.Contains(symbol, "/"):
		q, err = c.readFX(ctx, key, symbol)
	default:
		ticker := strings.TrimPrefix(symbol, "YAHOO:")
		if regexp.MustCompile("^\\d{4,6}(\\.TW|\\.TWO)?$").MatchString(ticker) && !strings.Contains(ticker, ".") {
			ticker += ".TW"
		}
		q, err = c.readYahoo(ctx, key, ticker)
	}
	if err != nil {
		return Quote{}, err
	}
	c.mu.Lock()
	c.cache[key] = cacheEntry{q, time.Now().Add(c.ttlFor(key))}
	c.mu.Unlock()
	return q, nil
}

func normalizeKey(symbol string) (string, error) {
	pair := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(symbol, "FX:"), "MAX:"), "BOT:")
	if strings.HasPrefix(symbol, "MAX:") {
		pair = strings.NewReplacer("/", "", "-", "", "_", "").Replace(pair)
		if !regexp.MustCompile("^[A-Z0-9]{5,12}$").MatchString(pair) {
			return "", &Error{"invalid_symbol", "Use MAX:BTC/TWD or another supported MAX market."}
		}
		return "MAX:" + pair, nil
	}
	if strings.HasPrefix(symbol, "BOT:") {
		if !regexp.MustCompile("^[A-Z]{3}$").MatchString(pair) {
			return "", &Error{"invalid_symbol", "Use BOT:USD or another three-letter currency code."}
		}
		return "BOT:" + pair, nil
	}
	if strings.HasPrefix(symbol, "FX:") || strings.Contains(symbol, "/") {
		pair = strings.ReplaceAll(pair, " ", "")
		if !regexp.MustCompile("^[A-Z]{3}/[A-Z]{3}$").MatchString(pair) {
			return "", &Error{"invalid_symbol", "Use FX:USD/TWD or a three-letter currency pair."}
		}
		return "FX:" + pair, nil
	}
	ticker := strings.TrimPrefix(symbol, "YAHOO:")
	if !regexp.MustCompile("^[A-Z0-9.^=_-]{1,20}(\\.(TW|TWO))?$").MatchString(ticker) {
		return "", &Error{"invalid_symbol", "Ticker contains unsupported characters."}
	}
	return "YAHOO:" + ticker, nil
}

func (c *Client) readYahoo(ctx context.Context, key, ticker string) (Quote, error) {
	var payload struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Currency  string  `json:"currency"`
					Name      string  `json:"longName"`
					ShortName string  `json:"shortName"`
					Price     float64 `json:"regularMarketPrice"`
					Timestamp int64   `json:"regularMarketTime"`
				} `json:"meta"`
			} `json:"result"`
		} `json:"chart"`
	}
	if err := c.getJSON(ctx, c.yahooURL+url.PathEscape(ticker)+"?range=1d&interval=1m", &payload); err != nil {
		return Quote{}, err
	}
	if len(payload.Chart.Result) == 0 {
		return Quote{}, &Error{"not_found", "Yahoo Finance returned no quote for " + ticker + "."}
	}
	meta := payload.Chart.Result[0].Meta
	if meta.Timestamp <= 0 || meta.Price <= 0 {
		return Quote{}, &Error{"not_found", "Yahoo Finance returned no current quote for " + ticker + "."}
	}
	name := meta.Name
	if name == "" {
		name = meta.ShortName
	}
	at := time.Unix(meta.Timestamp, 0).UTC()
	return Quote{Symbol: key, Name: name, Price: strconv.FormatFloat(meta.Price, 'f', -1, 64), Currency: meta.Currency, Timestamp: at, Source: "Yahoo Finance", Stale: stale(at, c.staleAfter(key))}, nil
}

func (c *Client) readMAX(ctx context.Context, key, raw string) (Quote, error) {
	pair := strings.NewReplacer("/", "", "-", "", "_", "").Replace(strings.ToLower(raw))
	var p struct {
		Last string `json:"last"`
		Buy  string `json:"buy"`
		Sell string `json:"sell"`
		At   int64  `json:"at"`
	}
	if err := c.getJSON(ctx, c.maxURL+url.PathEscape(pair), &p); err != nil {
		return Quote{}, err
	}
	if p.Last == "" && p.Buy == "" && p.Sell == "" {
		return Quote{}, &Error{"not_found", "MAX returned no quote for " + key + "."}
	}
	at := time.Now().UTC()
	if p.At > 0 {
		at = time.Unix(p.At, 0).UTC()
	}
	currency := ""
	for _, suffix := range []string{"USDT", "USDC", "TWD", "USD", "BTC", "ETH"} {
		if strings.HasSuffix(pair, strings.ToLower(suffix)) {
			currency = suffix
			break
		}
	}
	if currency == "" && len(pair) >= 3 {
		currency = strings.ToUpper(pair[len(pair)-3:])
	}
	return Quote{Symbol: key, Price: p.Last, CashBuy: p.Buy, CashSell: p.Sell, Currency: currency, Timestamp: at, Source: "MAX", Stale: stale(at, 15*time.Minute)}, nil
}

func (c *Client) readFX(ctx context.Context, key, raw string) (Quote, error) {
	parts := strings.Split(strings.TrimPrefix(raw, "FX:"), "/")
	if len(parts) != 2 {
		return Quote{}, &Error{"invalid_symbol", "Use FX:USD/TWD."}
	}
	endpoint, err := url.Parse(c.fxURL)
	if err != nil {
		return Quote{}, &Error{"unavailable", "FX source is not configured."}
	}
	query := endpoint.Query()
	query.Set("base", parts[0])
	query.Set("symbols", parts[1])
	endpoint.RawQuery = query.Encode()
	var p struct {
		Date  string             `json:"date"`
		Rates map[string]float64 `json:"rates"`
	}
	if err := c.getJSON(ctx, endpoint.String(), &p); err != nil {
		return Quote{}, err
	}
	rate, ok := p.Rates[parts[1]]
	if !ok || rate <= 0 {
		return Quote{}, &Error{"not_found", "FX source returned no rate for " + key + "."}
	}
	at, _ := time.Parse("2006-01-02", p.Date)
	if at.IsZero() {
		return Quote{}, &Error{"unavailable", "Frankfurter returned no rate date."}
	}
	at = at.UTC()
	return Quote{Symbol: key, Price: strconv.FormatFloat(rate, 'f', -1, 64), Currency: parts[1], Timestamp: at, Source: "Frankfurter", Stale: stale(at, 96*time.Hour)}, nil
}

func (c *Client) readBank(ctx context.Context, key, currency string) (Quote, error) {
	body, err := c.get(ctx, c.bankURL)
	if err != nil {
		return Quote{}, err
	}
	rows, err := bankRows(body)
	if err != nil {
		return Quote{}, &Error{"unavailable", "Bank of Taiwan rates could not be parsed."}
	}
	row, ok := rows[currency]
	if !ok {
		return Quote{}, &Error{"not_found", "Bank of Taiwan returned no cash/spot rates for " + currency + "."}
	}
	return Quote{Symbol: key, Currency: currency, CashBuy: row[0], CashSell: row[1], SpotBuy: row[2], SpotSell: row[3], Timestamp: time.Now().UTC(), Source: "Bank of Taiwan"}, nil
}

func (c *Client) getJSON(ctx context.Context, target string, dst any) error {
	body, err := c.get(ctx, target)
	if err != nil {
		return err
	}
	if json.Unmarshal(body, dst) != nil {
		return &Error{"unavailable", "Quote source returned invalid data."}
	}
	return nil
}

func (c *Client) timeoutFor(key string) time.Duration {
	limit := 8 * time.Second
	switch {
	case strings.HasPrefix(key, "MAX:") || strings.HasPrefix(key, "FX:"):
		limit = 5 * time.Second
	}
	if c.timeout > 0 && c.timeout < limit {
		return c.timeout
	}
	return limit
}

func (c *Client) ttlFor(key string) time.Duration {
	ttl := 30 * time.Second
	switch {
	case strings.HasPrefix(key, "MAX:"):
		ttl = 10 * time.Second
	case strings.HasPrefix(key, "FX:"):
		ttl = time.Hour
	case strings.HasPrefix(key, "BOT:"):
		ttl = 15 * time.Minute
	}
	if c.cacheTTL > 0 && c.cacheTTL < ttl {
		return c.cacheTTL
	}
	return ttl
}

func (c *Client) staleAfter(key string) time.Duration {
	switch {
	case strings.HasPrefix(key, "MAX:"):
		return 15 * time.Minute
	case strings.HasPrefix(key, "FX:"):
		return 96 * time.Hour
	case strings.HasPrefix(key, "YAHOO:") && strings.Contains(key, "-"):
		return 15 * time.Minute
	default:
		return 24 * time.Hour
	}
}

func stale(timestamp time.Time, maxAge time.Duration) bool {
	return timestamp.IsZero() || time.Since(timestamp) > maxAge || timestamp.After(time.Now().Add(5*time.Minute))
}

func (c *Client) get(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, &Error{"unavailable", "Could not prepare quote request."}
	}
	req.Header.Set("User-Agent", "abot/1 quote-reader")
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &Error{"timeout", "Quote request timed out."}
		}
		if ctx.Err() != nil {
			return nil, &Error{"cancelled", "Quote request was cancelled."}
		}
		return nil, &Error{"unavailable", "Quote source request failed."}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &Error{"rate_limit", "Quote source rate-limited the request; retry later."}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &Error{"unavailable", fmt.Sprintf("Quote source returned HTTP %d.", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil || int64(len(body)) > c.maxBytes {
		return nil, &Error{"limit", "Quote source response exceeded its size limit."}
	}
	return body, nil
}

func bankRows(data []byte) (map[string][4]string, error) {
	root, err := xhtml.Parse(strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	rows := map[string][4]string{}
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && n.Data == "tr" {
			cells := map[string]string{}
			var cell func(*xhtml.Node)
			cell = func(child *xhtml.Node) {
				if child.Type == xhtml.ElementNode && (child.Data == "td" || child.Data == "th") {
					if field := attr(child, "data-table"); field != "" {
						cells[field] = strings.TrimSpace(nodeText(child))
					}
				}
				for sub := child.FirstChild; sub != nil; sub = sub.NextSibling {
					cell(sub)
				}
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				cell(child)
			}
			currency := strings.Fields(strings.ReplaceAll(cells["Currency"], "/", " "))
			if len(currency) > 0 {
				v := [4]string{cells["Cash Rate Buy"], cells["Cash Rate Sell"], cells["Spot Rate Buy"], cells["Spot Rate Sell"]}
				if v[0] != "" || v[2] != "" {
					rows[strings.ToUpper(currency[0])] = v
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if len(rows) == 0 {
		return nil, errors.New("no rates")
	}
	return rows, nil
}
func attr(n *xhtml.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}
func nodeText(n *xhtml.Node) string {
	var out strings.Builder
	var walk func(*xhtml.Node)
	walk = func(cur *xhtml.Node) {
		if cur.Type == xhtml.TextNode {
			out.WriteString(cur.Data)
		}
		for ch := cur.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return out.String()
}

func (q Quote) String() string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s", q.Symbol)
	if q.Name != "" {
		fmt.Fprintf(&out, " — %s", q.Name)
	}
	if q.Price != "" {
		fmt.Fprintf(&out, ": %s %s", q.Price, q.Currency)
	}
	if q.CashBuy != "" || q.CashSell != "" {
		fmt.Fprintf(&out, "\nCash buy/sell: %s / %s %s", q.CashBuy, q.CashSell, q.Currency)
	}
	if q.SpotBuy != "" || q.SpotSell != "" {
		fmt.Fprintf(&out, "\nSpot buy/sell: %s / %s %s", q.SpotBuy, q.SpotSell, q.Currency)
	}
	fmt.Fprintf(&out, "\n%s · %s", q.Source, q.Timestamp.UTC().Format(time.RFC3339))
	if q.Stale {
		out.WriteString(" · stale data")
	}
	return out.String()
}
