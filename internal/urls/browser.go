package urls

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func retryWithBrowser(err error) bool {
	switch sourceErrorCategory(err) {
	case "http_status", "unsupported_source", "extraction_failed":
		return true
	default:
		return false
	}
}

func (r *Reader) readBrowser(ctx context.Context, target string) (Result, error) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" {
		return Result{}, &Error{Category: "unsupported_source", Message: "The browser fallback supports HTTPS pages only."}
	}
	proxy, stopProxy, err := r.startHTTPSProxy(ctx)
	if err != nil {
		return Result{}, &Error{Category: "extraction_failed", Message: "Could not start the restricted browser proxy."}
	}
	defer stopProxy()
	profile, err := os.MkdirTemp("", "abot-browser-")
	if err != nil {
		return Result{}, &Error{Category: "extraction_failed", Message: "Could not create a private browser profile."}
	}
	defer os.RemoveAll(profile)
	command := exec.CommandContext(ctx, r.config.BrowserExecutable,
		"--headless=new", "--disable-gpu", "--disable-background-networking", "--disable-extensions",
		"--disable-sync", "--metrics-recording-only", "--no-first-run", "--no-default-browser-check",
		"--disable-component-update", "--disable-features=OptimizationHints,MediaRouter",
		"--user-data-dir="+profile, "--remote-debugging-port=0", "--proxy-server="+proxy,
		"--proxy-bypass-list=<-loopback>", "--host-resolver-rules=MAP * ~NOTFOUND,EXCLUDE 127.0.0.1",
		"about:blank",
	)
	command.Env = []string{"HOME=" + profile, "PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8"}
	command.Stdout, command.Stderr = nilWriter{}, nilWriter{}
	if err := command.Start(); err != nil {
		return Result{}, &Error{Category: "unsupported_source", Message: "Configured Chromium browser is unavailable."}
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	defer func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		select {
		case <-wait:
		case <-time.After(time.Second):
		}
	}()

	port, browserPath, err := waitBrowserEndpoint(ctx, profile, wait)
	if err != nil {
		return Result{}, err
	}
	endpoint := "ws://127.0.0.1:" + strconv.Itoa(port) + browserPath
	dialer := websocket.Dialer{Proxy: nil, HandshakeTimeout: 3 * time.Second}
	connection, _, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, contextError(ctx.Err())
		}
		return Result{}, &Error{Category: "extraction_failed", Message: "Could not connect to the local browser debugging protocol."}
	}
	defer connection.Close()
	readLimit := r.config.MaxBytes*2 + (1 << 20)
	connection.SetReadLimit(readLimit)
	cdp := browserCDP{conn: connection, sessionID: ""}
	created, err := cdp.call(ctx, "Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		return Result{}, browserContextError(ctx, err)
	}
	var targetResult struct {
		TargetID string `json:"targetId"`
	}
	if json.Unmarshal(created, &targetResult) != nil || targetResult.TargetID == "" {
		return Result{}, &Error{Category: "extraction_failed", Message: "The browser did not create a page target."}
	}
	attached, err := cdp.call(ctx, "Target.attachToTarget", map[string]any{"targetId": targetResult.TargetID, "flatten": true})
	if err != nil {
		return Result{}, browserContextError(ctx, err)
	}
	var attachedResult struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(attached, &attachedResult) != nil || attachedResult.SessionID == "" {
		return Result{}, &Error{Category: "extraction_failed", Message: "The browser did not attach a page session."}
	}
	cdp.sessionID = attachedResult.SessionID
	if _, err := cdp.call(ctx, "Page.enable", nil); err != nil {
		return Result{}, browserContextError(ctx, err)
	}
	if _, err := cdp.call(ctx, "Runtime.enable", nil); err != nil {
		return Result{}, browserContextError(ctx, err)
	}
	if _, err := cdp.call(ctx, "Page.navigate", map[string]any{"url": target}); err != nil {
		return Result{}, browserContextError(ctx, err)
	}
	if err := cdp.waitEvent(ctx, "Page.loadEventFired"); err != nil {
		return Result{}, browserContextError(ctx, err)
	}
	value, err := cdp.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "({url: location.href, title: document.title, text: document.body ? document.body.innerText : ''})",
		"returnByValue": true, "awaitPromise": true,
	})
	if err != nil {
		return Result{}, browserContextError(ctx, err)
	}
	var evaluation struct {
		Result struct {
			Value struct {
				URL   string `json:"url"`
				Title string `json:"title"`
				Text  string `json:"text"`
			} `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(value, &evaluation) != nil {
		return Result{}, &Error{Category: "extraction_failed", Message: "Browser page text could not be read."}
	}
	content := normalizeText(evaluation.Result.Value.Text)
	if content == "" {
		return Result{}, &Error{Category: "extraction_failed", Message: "Browser page contained no readable text."}
	}
	finalURL := evaluation.Result.Value.URL
	if finalURL == "" {
		finalURL = target
	}
	if _, _, err := r.validateURL(ctx, finalURL); err != nil {
		return Result{}, err
	}
	return Result{URL: target, FinalURL: finalURL, Source: "chromium", Title: strings.TrimSpace(evaluation.Result.Value.Title), Content: truncate(content, r.config.MaxOutputBytes)}, nil
}

type browserCDP struct {
	conn      *websocket.Conn
	sessionID string
	nextID    int
	events    []string
}

func (c *browserCDP) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	c.nextID++
	request := map[string]any{"id": c.nextID, "method": method, "params": params}
	if c.sessionID != "" {
		request["sessionId"] = c.sessionID
	}
	if err := c.conn.WriteJSON(request); err != nil {
		return nil, err
	}
	for {
		var message struct {
			ID        int             `json:"id"`
			Method    string          `json:"method"`
			SessionID string          `json:"sessionId"`
			Result    json.RawMessage `json:"result"`
			Error     json.RawMessage `json:"error"`
		}
		if err := c.read(ctx, &message); err != nil {
			return nil, err
		}
		if message.Method != "" {
			c.events = append(c.events, message.Method)
			continue
		}
		if message.ID != c.nextID {
			continue
		}
		if message.SessionID != c.sessionID {
			continue
		}
		if len(message.Error) != 0 {
			return nil, errors.New("browser protocol command failed")
		}
		return message.Result, nil
	}
}

func (c *browserCDP) waitEvent(ctx context.Context, method string) error {
	for index, event := range c.events {
		if event == method {
			c.events = append(c.events[:index], c.events[index+1:]...)
			return nil
		}
	}
	for {
		var message struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := c.read(ctx, &message); err != nil {
			return err
		}
		if message.Method == method {
			return nil
		}
		if message.Method != "" {
			c.events = append(c.events, message.Method)
		}
	}
}

func (c *browserCDP) read(ctx context.Context, value any) error {
	deadline := time.Now().Add(15 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = c.conn.SetReadDeadline(deadline)
	if err := c.conn.ReadJSON(value); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

func waitBrowserEndpoint(ctx context.Context, profile string, exited <-chan error) (int, string, error) {
	file := filepath.Join(profile, "DevToolsActivePort")
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if data, err := os.ReadFile(file); err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) == 2 {
				port, parseErr := strconv.Atoi(lines[0])
				if parseErr == nil && port > 0 && strings.HasPrefix(lines[1], "/devtools/browser/") {
					return port, lines[1], nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return 0, "", contextError(ctx.Err())
		case <-timer.C:
			return 0, "", &Error{Category: "timeout", Message: "Headless browser startup timed out."}
		case <-exited:
			return 0, "", &Error{Category: "extraction_failed", Message: "Headless browser exited before becoming ready."}
		case <-ticker.C:
		}
	}
}

func browserContextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return contextError(ctx.Err())
	}
	return &Error{Category: "extraction_failed", Message: fmt.Sprintf("Headless browser extraction failed (%T).", err)}
}

type nilWriter struct{}

func (nilWriter) Write(value []byte) (int, error) { return len(value), nil }
