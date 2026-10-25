package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSearchFiltersActivePublicMarketsAndBoundsResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "limit=100") {
			t.Errorf("query=%q", r.URL.RawQuery)
		}
		fmt.Fprint(w, "[{\"question\":\"Will it rain?\",\"slug\":\"rain\",\"active\":true,\"closed\":false},{\"question\":\"Will Bitcoin rise?\",\"slug\":\"btc\",\"active\":true,\"closed\":false},{\"question\":\"Bitcoin history\",\"slug\":\"old\",\"active\":true,\"closed\":true}]")
	}))
	defer server.Close()
	client := NewClient()
	client.APIURL = server.URL
	client.HTTP = server.Client()
	markets, err := client.Search(context.Background(), "bitcoin", 5)
	if err != nil || len(markets) != 1 || markets[0].Slug != "btc" || markets[0].URL() != "https://polymarket.com/event/btc" {
		t.Fatalf("markets=%#v err=%v", markets, err)
	}
	if _, err := client.Search(context.Background(), "x", 11); err == nil {
		t.Fatal("accepted excessive result count")
	}
}

func TestWatchSubscribesAndStopsOnCancellation(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		var request struct {
			Assets []string `json:"assets_ids"`
			Type   string   `json:"type"`
		}
		if json.Unmarshal(data, &request) != nil || request.Type != "market" || len(request.Assets) != 1 || request.Assets[0] != "123" {
			t.Errorf("subscription=%s", data)
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte("{\"event_type\":\"price_change\"}"))
		<-r.Context().Done()
	}))
	defer server.Close()
	client := NewClient()
	client.Timeout = time.Second
	client.WSURL = "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	received := false
	err := client.Watch(ctx, []string{"123"}, func(event string) { received = strings.Contains(event, "price_change"); cancel() })
	if err != nil || !received {
		t.Fatalf("received=%t err=%v", received, err)
	}
	if err := client.Watch(context.Background(), []string{"duplicate", "duplicate"}, func(string) {}); err == nil {
		t.Fatal("accepted invalid asset IDs")
	}
}
