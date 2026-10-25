package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type MorselClient struct {
	endpoint string
	apiKey   string
	client   *http.Client
}

func NewMorselClient(endpoint, apiKey string, client *http.Client) (*MorselClient, error) {
	endpoint, apiKey = strings.TrimSpace(endpoint), strings.TrimSpace(apiKey)
	u, err := url.Parse(endpoint)
	if endpoint == "" || apiKey == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || client == nil {
		return nil, errors.New("Morsel publish URL, API key, and HTTP client are required")
	}
	return &MorselClient{endpoint: endpoint, apiKey: apiKey, client: client}, nil
}

func NewConfiguredMorsel(endpoint, apiKey string) (*MorselClient, error) {
	return NewMorselClient(endpoint, apiKey, &http.Client{Timeout: 30 * time.Second})
}

func (m *MorselClient) Publish(ctx context.Context, title, content string) (string, error) {
	payload, err := json.Marshal(struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}{Title: title, Content: content})
	if err != nil {
		return "", errors.New("encode Morsel content")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", errors.New("create Morsel publish request")
	}
	request.Header.Set("Authorization", "Bearer "+m.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return "", errors.New("call Morsel publish API failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Morsel publish API returned HTTP %d", response.StatusCode)
	}
	var result struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil || result.URL == "" {
		return "", errors.New("Morsel publish API returned no URL")
	}
	return result.URL, nil
}
