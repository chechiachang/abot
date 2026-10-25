package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chechiachang/abot/internal/engine"
	"github.com/chechiachang/abot/internal/policy"
	"github.com/chechiachang/abot/internal/quotes"
	agentruntime "github.com/chechiachang/abot/internal/runtime"
)

func TestTelegramQuoteCommandRequiresPolicyAndReturnsNormalizedQuote(t *testing.T) {
	state, err := agentruntime.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	var reply string
	var messageID int
	telegramServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body SendMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		reply = body.Text
		messageID++
		fmt.Fprintf(w, "{\"ok\":true,\"result\":{\"message_id\":%d,\"chat\":{\"id\":10,\"type\":\"private\"}}}", 899+messageID)
	}))
	defer telegramServer.Close()
	telegramClient, err := NewClientWithHTTP("token", telegramServer.URL, telegramServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	var quoteCalls int
	quoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quoteCalls++
		fmt.Fprint(w, "{\"chart\":{\"result\":[{\"meta\":{\"currency\":\"TWD\",\"regularMarketPrice\":1200,\"regularMarketTime\":1700000000}}]}}")
	}))
	defer quoteServer.Close()
	quoteHTTP := quoteServer.Client()
	quoteHTTP.Transport = quoteHostTransport{target: quoteServer.URL, base: quoteHTTP.Transport}
	quoteClient := quotes.NewClientWithHTTP(quoteHTTP, time.Second, time.Minute, 1024*1024)
	current := policy.Policy{Version: policy.Version, Telegram: policy.Telegram{AllowedUserIDs: []int64{10}},
		Global:   policy.Capabilities{Tools: []string{"read_quotes"}, Models: []string{"model"}},
		Defaults: policy.Selection{Tools: []string{"read_quotes"}, Model: "model"}}
	bridge := Bridge{State: state, Client: telegramClient, Quotes: quoteClient,
		Engine: &engine.Engine{State: state, LoadPolicy: func() (policy.Policy, error) { return current, nil }}}
	message := &Message{MessageID: 1, Chat: Chat{ID: 10, Type: "private"}, From: &User{ID: 10}, Text: "/t 2330"}
	if err := bridge.handleUpdate(context.Background(), Update{UpdateID: 1, Message: message}); err != nil {
		t.Fatal(err)
	}
	if quoteCalls != 1 || !strings.Contains(reply, "YAHOO:2330") || !strings.Contains(reply, "1200 TWD") || !strings.Contains(reply, "Yahoo Finance") {
		t.Fatalf("calls=%d reply=%q", quoteCalls, reply)
	}
	current.Defaults.Tools = nil
	reply = ""
	message.MessageID = 2
	if err := bridge.handleUpdate(context.Background(), Update{UpdateID: 2, Message: message}); err != nil {
		t.Fatal(err)
	}
	if quoteCalls != 1 || !strings.Contains(reply, "not enabled") {
		t.Fatalf("disabled quote calls=%d reply=%q", quoteCalls, reply)
	}
}

type quoteHostTransport struct {
	target string
	base   http.RoundTripper
}

func (r quoteHostTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	baseURL, err := url.Parse(r.target)
	if err != nil {
		return nil, err
	}
	baseURL.Path = "/" + strings.TrimPrefix(copy.URL.Path, "/v8/finance/chart/")
	baseURL.RawQuery = copy.URL.RawQuery
	copy.URL = baseURL
	copy.Host = baseURL.Host
	return r.base.RoundTrip(copy)
}
