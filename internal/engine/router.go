package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chechiachang/abot/internal/policy"
)

// Router output is advisory; only an exact policy-approved option is accepted.
type RouterConfig struct {
	Enabled  bool          `mapstructure:"enabled"`
	Endpoint string        `mapstructure:"endpoint"`
	Model    string        `mapstructure:"model"`
	APIKey   string        `mapstructure:"api_key"`
	Timeout  time.Duration `mapstructure:"timeout"`
}
type routeOption struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}
type routeRequest struct {
	Model   string        `json:"model"`
	Input   string        `json:"input"`
	Options []routeOption `json:"options"`
}
type routeResponse struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

func recommend(ctx context.Context, cfg RouterConfig, prompt string, base policy.Selection, p policy.Policy) policy.Selection {
	if !cfg.Enabled {
		return base
	}
	options := make([]routeOption, 0, len(p.Global.Models)*len(p.Global.ThinkingLevels))
	for _, modelName := range p.Global.Models {
		levels := p.ModelThinkingLevels[modelName]
		if len(levels) == 0 {
			levels = []string{""}
		}
		for _, effort := range levels {
			options = append(options, routeOption{modelName, effort})
		}
	}
	if len(options) < 2 {
		return base
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, err := json.Marshal(routeRequest{cfg.Model, routeInput(prompt), options})
	if err != nil {
		return base
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return base
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("api-key", cfg.APIKey)
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return base
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return base
	}
	var result routeResponse
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result) != nil {
		return base
	}
	for _, option := range options {
		if result.Model == option.Model && result.Effort == option.Effort {
			selected := base
			selected.Model, selected.ThinkingLevel = option.Model, option.Effort
			return selected
		}
	}
	return base
}
func routeInput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4096 {
		s = s[:4096]
	}
	return s
}
