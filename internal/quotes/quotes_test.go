package quotes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	client := NewClientWithHTTP(server.Client(), time.Second, time.Minute, 1<<20)
	client.yahooURL = server.URL + "/yahoo/"
	client.maxURL = server.URL + "/max/"
	client.fxURL = server.URL + "/fx"
	client.bankURL = server.URL + "/bank"
	return client, server
}

func TestYahooTaiwanStockAndCache(t *testing.T) {
	var calls atomic.Int32
	client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/yahoo/2330.TW" {
			t.Errorf("Yahoo path=%q", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"chart":{"result":[{"meta":{"currency":"TWD","longName":"Taiwan Semiconductor","regularMarketPrice":1234.5,"regularMarketTime":1700000000}}]}}`)
	})
	defer server.Close()
	quote, err := client.Read(context.Background(), "2330")
	if err != nil || quote.Symbol != "YAHOO:2330" || quote.Price != "1234.5" || quote.Currency != "TWD" || quote.Source != "Yahoo Finance" || !quote.Stale || !strings.Contains(quote.String(), "stale data") {
		t.Fatalf("quote=%#v err=%v", quote, err)
	}
	if _, err := client.Read(context.Background(), "2330"); err != nil || calls.Load() != 1 {
		t.Fatalf("cached quote calls=%d err=%v", calls.Load(), err)
	}
}

func TestMAXFrankfurterAndBankOfTaiwan(t *testing.T) {
	client, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/max/btctwd":
			_, _ = fmt.Fprint(w, `{"last":"2500000","buy":"2499000","sell":"2501000","at":1700000000}`)
		case "/fx":
			if r.URL.Query().Get("base") != "USD" || r.URL.Query().Get("symbols") != "TWD" {
				t.Errorf("FX query=%v", r.URL.Query())
			}
			_, _ = fmt.Fprint(w, `{"date":"2026-10-25","rates":{"TWD":31.5}}`)
		case "/bank":
			_, _ = fmt.Fprint(w, `<table><tr><td data-table="Currency">USD / US Dollar</td><td data-table="Cash Rate Buy">31.1</td><td data-table="Cash Rate Sell">32.1</td><td data-table="Spot Rate Buy">31.4</td><td data-table="Spot Rate Sell">31.8</td></tr></table>`)
		default:
			t.Errorf("unexpected quote endpoint %q", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	defer server.Close()
	for _, test := range []struct {
		symbol string
		check  func(Quote) bool
	}{
		{"MAX:BTC/TWD", func(q Quote) bool { return q.Price == "2500000" && q.Currency == "TWD" && q.Source == "MAX" && q.Stale }},
		{"FX:USD/TWD", func(q Quote) bool { return q.Price == "31.5" && q.Currency == "TWD" && q.Source == "Frankfurter" }},
		{"BOT:USD", func(q Quote) bool { return q.CashBuy == "31.1" && q.SpotSell == "31.8" && q.Source == "Bank of Taiwan" }},
	} {
		quote, err := client.Read(context.Background(), test.symbol)
		if err != nil || !test.check(quote) {
			t.Errorf("%s quote=%#v err=%v", test.symbol, quote, err)
		}
	}
}

func TestQuoteErrorsAndInputValidation(t *testing.T) {
	client, server := testClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
	defer server.Close()
	if _, err := client.Read(context.Background(), "AAPL"); errorCategory(err) != "rate_limit" {
		t.Fatalf("rate limit error=%v", err)
	}
	for _, symbol := range []string{"", "BOT:US", "FX:US/TWD", "MAX:../oops", "https://example.com"} {
		if _, err := client.Read(context.Background(), symbol); err == nil {
			t.Errorf("invalid symbol %q accepted", symbol)
		}
	}
}

func TestMissingSourceValuesAreNotInvented(t *testing.T) {
	for _, test := range []struct {
		name     string
		symbol   string
		response string
		want     string
	}{
		{"Yahoo quote", "AAPL", `{"chart":{"result":[]}}`, "not_found"},
		{"MAX quote", "MAX:BTC/TWD", `{}`, "not_found"},
		{"Frankfurter rate", "FX:USD/TWD", `{"date":"2026-10-25","rates":{}}`, "not_found"},
		{"Frankfurter date", "FX:USD/TWD", `{"rates":{"TWD":31.5}}`, "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, server := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, test.response) })
			defer server.Close()
			if quote, err := client.Read(context.Background(), test.symbol); errorCategory(err) != test.want || quote.Price != "" {
				t.Fatalf("quote=%#v err=%v", quote, err)
			}
		})
	}
}

func errorCategory(err error) string {
	if typed, ok := err.(*Error); ok {
		return typed.Category
	}
	return ""
}
