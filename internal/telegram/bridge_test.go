package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chechiachang/abot/internal/config"
	"github.com/chechiachang/abot/internal/engine"
	"github.com/chechiachang/abot/internal/media"
	"github.com/chechiachang/abot/internal/policy"
	agentruntime "github.com/chechiachang/abot/internal/runtime"
)

func TestBridgeStoresOnlyAuthorizedUnaddressedGroupContextAndHandlesAllowRequests(t *testing.T) {
	state, err := agentruntime.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	var sent int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/sendMessage" {
			t.Errorf("Telegram method path=%q", r.URL.Path)
		}
		sent++
		var body SendMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		var result map[string]any
		_ = json.Unmarshal([]byte(`{"message_id":900,"chat":{"id":123,"type":"private"}}`), &result)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	defer server.Close()
	client, err := NewClientWithHTTP("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	current := policy.Policy{
		Version: policy.Version, Telegram: policy.Telegram{AllowedUserIDs: []int64{10}, DefaultGroupID: -100},
		Global:   policy.Capabilities{Tools: []string{}, Models: []string{"model"}},
		Defaults: policy.Selection{Model: "model"},
	}
	cfg := config.Config{Telegram: config.TelegramConfig{AllowedUserIDs: []int64{10}, DefaultGroupID: -100}}
	host := &engine.Engine{State: state, Turns: agentruntime.NewTurnManager(), LoadPolicy: func() (policy.Policy, error) { return current, nil }}
	bridge := Bridge{Config: cfg, State: state, Engine: host, Client: client, username: "abot", workers: map[string]chan struct{}{}}
	ctx := context.Background()
	if err := bridge.handleUpdate(ctx, Update{UpdateID: 1, Message: &Message{MessageID: 1, Chat: Chat{ID: -100, Type: "supergroup"}, From: &User{ID: 10}, Text: "background detail", Date: 100}}); err != nil {
		t.Fatal(err)
	}
	for _, update := range []Update{
		{UpdateID: 2, Message: &Message{MessageID: 2, Chat: Chat{ID: -100, Type: "group"}, From: &User{ID: 11}, Text: "not allowed", Date: 101}},
		{UpdateID: 3, Message: &Message{MessageID: 3, Chat: Chat{ID: -200, Type: "group"}, From: &User{ID: 10}, Text: "wrong group", Date: 102}},
		{UpdateID: 4, Message: &Message{MessageID: 4, Chat: Chat{ID: -100, Type: "group"}, Text: "anonymous", Date: 103}},
	} {
		if err := bridge.handleUpdate(ctx, update); err != nil {
			t.Fatal(err)
		}
	}
	passive, err := state.RecentPassiveMessages(ctx, -100, 10)
	if err != nil || len(passive) != 1 || passive[0].Text != "background detail" {
		t.Fatalf("passive messages=%#v err=%v", passive, err)
	}
	allow := Update{UpdateID: 5, Message: &Message{MessageID: 5, Chat: Chat{ID: 123, Type: "private"}, From: &User{ID: 123}, Text: "/allow"}}
	if err := bridge.handleUpdate(ctx, allow); err != nil {
		t.Fatal(err)
	}
	requests, err := state.ListAccessRequests(ctx, "pending")
	if err != nil || len(requests) != 1 || requests[0].UserID != 123 || sent != 1 {
		t.Fatalf("access requests=%#v sent=%d err=%v", requests, sent, err)
	}
}

func TestBridgeQueuesAuthorizedImagesAndIndexesApprovedChannelPostsWithoutTurns(t *testing.T) {
	state, err := agentruntime.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	fileBytes := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bottoken/getFile":
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{"file_id":"photo","file_size":10,"file_path":"photos/image.jpg"}}`)
		case "/file/bottoken/photos/image.jpg":
			_, _ = w.Write(fileBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClientWithHTTP("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	mediaStore, err := media.NewStore(filepath.Join(t.TempDir(), "media"), media.Limits{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	current := policy.Policy{
		Version:  policy.Version,
		Telegram: policy.Telegram{AllowedUserIDs: []int64{10}, DefaultGroupID: -100, ApprovedChannelIDs: []int64{-300}},
		Global:   policy.Capabilities{Models: []string{"model"}}, Defaults: policy.Selection{Model: "model"},
	}
	host := &engine.Engine{State: state, Turns: agentruntime.NewTurnManager(), LoadPolicy: func() (policy.Policy, error) { return current, nil }}
	bridge := Bridge{
		Config: config.Config{Media: config.MediaConfig{MaxBytes: 1024, MaxDuration: time.Minute}}, State: state,
		Engine: host, Media: mediaStore, Client: client, username: "abot",
		workers: map[string]chan struct{}{turnWorkerKey("-100", "main"): make(chan struct{}, 1)},
	}
	ctx := context.Background()
	userMessage := &Message{MessageID: 7, Chat: Chat{ID: -100, Type: "group"}, From: &User{ID: 10}, Text: "@abot describe this", Photo: []PhotoSize{{FileID: "photo", FileSize: 10}}}
	if err := bridge.handleUpdate(ctx, Update{UpdateID: 1, Message: userMessage}); err != nil {
		t.Fatal(err)
	}
	queued, err := state.PendingInputs(ctx, 10)
	if err != nil || len(queued) != 1 || queued[0].AttachmentIDs == "" {
		t.Fatalf("queued image input=%#v err=%v", queued, err)
	}
	owned, err := state.InputAttachments(ctx, queued[0].ID, "-100", "10", []int64{-300})
	if err != nil || len(owned) != 1 || owned[0].Kind != "image" {
		t.Fatalf("user image attachments=%#v err=%v", owned, err)
	}

	channelPost := &Message{MessageID: 44, Chat: Chat{ID: -300, Type: "channel"}, Photo: []PhotoSize{{FileID: "photo", FileSize: 10}}}
	if err := bridge.handleUpdate(ctx, Update{UpdateID: 2, ChannelPost: channelPost}); err != nil {
		t.Fatal(err)
	}
	indexed, err := state.AttachmentsForChannel(ctx, -300, 44)
	if err != nil || len(indexed) != 1 || !indexed[0].IndexedChannel {
		t.Fatalf("indexed channel images=%#v err=%v", indexed, err)
	}
	queuedAfterIndex, err := state.PendingInputs(ctx, 10)
	if err != nil || len(queuedAfterIndex) != 1 {
		t.Fatalf("channel post unexpectedly started an agent turn: inputs=%#v err=%v", queuedAfterIndex, err)
	}
	if info, err := os.Stat(indexed[0].Path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("channel artifact mode=%v err=%v", info, err)
	}
}

func TestGroupCommandsRequireBotTargetExceptAllow(t *testing.T) {
	name, _, command, targeted := parseCommand("/help", "abot")
	if !command || targeted || name != "help" {
		t.Fatalf("untargeted command parsed as %q targeted=%t command=%t", name, targeted, command)
	}
	name, _, command, targeted = parseCommand("/help@abot", "abot")
	if !command || !targeted || name != "help" {
		t.Fatalf("targeted command parsed as %q targeted=%t command=%t", name, targeted, command)
	}
}

type articleTestPublisher struct{}

func (articleTestPublisher) Publish(context.Context, string, string) (string, error) {
	return "https://morsel.example/article/1", nil
}

func TestArticleCommandQueuesTextAndURLRequestsOnlyWithToolPermission(t *testing.T) {
	state, err := agentruntime.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/sendMessage" {
			t.Errorf("Telegram method path=%q", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":900,"chat":{"id":123,"type":"private"}}}`)
	}))
	defer server.Close()
	client, err := NewClientWithHTTP("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	current := policy.Policy{Version: policy.Version, Telegram: policy.Telegram{AllowedUserIDs: []int64{10}},
		Global:   policy.Capabilities{Tools: []string{"read_url_content"}, Models: []string{"model"}},
		Defaults: policy.Selection{Tools: []string{"read_url_content"}, Model: "model"}}
	bridge := Bridge{Config: config.Config{}, State: state, Client: client, Publisher: articleTestPublisher{},
		Engine:  &engine.Engine{State: state, Turns: agentruntime.NewTurnManager(), LoadPolicy: func() (policy.Policy, error) { return current, nil }},
		workers: map[string]chan struct{}{turnWorkerKey("10", "main"): make(chan struct{}, 1)}, username: "abot"}
	message := &Message{MessageID: 7, Chat: Chat{ID: 10, Type: "private"}, From: &User{ID: 10}, Text: "/f cover this topic https://example.org/story"}
	if err := bridge.handleUpdate(context.Background(), Update{UpdateID: 4, Message: message}); err != nil {
		t.Fatal(err)
	}
	queued, err := state.PendingInputs(context.Background(), 10)
	if err != nil || len(queued) != 1 {
		t.Fatalf("queued=%#v err=%v", queued, err)
	}
	var sourceURLs []string
	if err := json.Unmarshal([]byte(queued[0].SourceURLs), &sourceURLs); err != nil || len(sourceURLs) != 1 || sourceURLs[0] != "https://example.org/story" || queued[0].Kind != agentruntime.InputArticle || !queued[0].PublishMorsel {
		t.Fatalf("article input=%#v sources=%#v err=%v", queued[0], sourceURLs, err)
	}
	if !strings.Contains(queued[0].Text, "cover this topic") || queued[0].PublishTitle != "cover this topic" {
		t.Fatalf("queued article text=%q title=%q", queued[0].Text, queued[0].PublishTitle)
	}
}

func TestArticleCommandRequiresURLToolPermission(t *testing.T) {
	state, err := agentruntime.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	var replies int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		replies++
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":900,"chat":{"id":123,"type":"private"}}}`)
	}))
	defer server.Close()
	client, err := NewClientWithHTTP("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	current := policy.Policy{Version: policy.Version, Telegram: policy.Telegram{AllowedUserIDs: []int64{10}},
		Global: policy.Capabilities{Tools: []string{}, Models: []string{"model"}}, Defaults: policy.Selection{Model: "model"}}
	bridge := Bridge{State: state, Client: client, Publisher: articleTestPublisher{},
		Engine: &engine.Engine{State: state, Turns: agentruntime.NewTurnManager(), LoadPolicy: func() (policy.Policy, error) { return current, nil }}}
	message := &Message{MessageID: 8, Chat: Chat{ID: 10, Type: "private"}, From: &User{ID: 10}, Text: "/f https://example.org/story"}
	if err := bridge.handleUpdate(context.Background(), Update{UpdateID: 5, Message: message}); err != nil {
		t.Fatal(err)
	}
	queued, err := state.PendingInputs(context.Background(), 10)
	if err != nil || len(queued) != 0 || replies != 1 {
		t.Fatalf("URL article bypassed policy: queued=%#v replies=%d err=%v", queued, replies, err)
	}
}

func TestPublishedLinkEscapesHTMLAndRejectsUnsafeTargets(t *testing.T) {
	got, err := PublishedLink("https://morsel.example/article?a=1&b=2")
	if err != nil || !strings.Contains(got, "&amp;") {
		t.Fatalf("published link=%q err=%v", got, err)
	}
	for _, target := range []string{"http://morsel.example/a", "https://user@morsel.example/a", "javascript:alert(1)"} {
		if _, err := PublishedLink(target); err == nil {
			t.Errorf("PublishedLink(%q) succeeded", target)
		}
	}
}

type countingArticlePublisher struct{ calls int }

func (p *countingArticlePublisher) Publish(context.Context, string, string) (string, error) {
	p.calls++
	return "https://morsel.example/article/partial", nil
}

func TestArticleSourceFailureNeverPublishesPartialContent(t *testing.T) {
	state, err := agentruntime.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	var messageID int64 = 100
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messageID++
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{
			"message_id": messageID, "chat": map[string]any{"id": 10, "type": "private"},
		}})
	}))
	defer server.Close()
	client, err := NewClientWithHTTP("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	current := policy.Policy{Version: policy.Version, Telegram: policy.Telegram{AllowedUserIDs: []int64{10}},
		Global:   policy.Capabilities{Tools: []string{"read_url_content"}, Models: []string{"model"}},
		Defaults: policy.Selection{Tools: []string{"read_url_content"}, Model: "model"}}
	publisher := &countingArticlePublisher{}
	bridge := Bridge{Config: config.Config{}, State: state, Client: client, Publisher: publisher,
		Engine: &engine.Engine{State: state, Turns: agentruntime.NewTurnManager(), LoadPolicy: func() (policy.Policy, error) { return current, nil }}}
	bridge.runInput(context.Background(), agentruntime.QueuedInput{
		ID: "article-failure", ChatID: "10", UserID: "10", SenderID: 10, BranchID: "main",
		Kind: agentruntime.InputArticle, Text: "rewrite", SourceURLs: `["http://127.0.0.1/secret"]`, PublishMorsel: true,
	})
	if publisher.calls != 0 {
		t.Fatalf("published %d partial articles after source failure", publisher.calls)
	}
}
