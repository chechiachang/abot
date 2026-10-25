package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/toolconfirmation"
	"google.golang.org/genai"

	"github.com/chechiachang/abot/internal/config"
)

func TestHTTPMCPDiscoveryDispatchAuthorizationAndCredentials(t *testing.T) {
	t.Setenv("MCP_TEST_TOKEN", "test-secret")
	var calls atomic.Int32
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	sdk.AddTool[struct {
		Message string `json:"message"`
	}, struct {
		Reply string `json:"reply"`
	}](server, &sdk.Tool{Name: "echo", Description: "echo input"}, func(_ context.Context, _ *sdk.CallToolRequest, input struct {
		Message string `json:"message"`
	}) (*sdk.CallToolResult, struct {
		Reply string `json:"reply"`
	}, error) {
		calls.Add(1)
		return nil, struct {
			Reply string `json:"reply"`
		}{Reply: input.Message}, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	var authRequests atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "test-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		authRequests.Add(1)
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	var allowed atomic.Bool
	allowed.Store(true)
	var observed atomic.Int32
	manager := NewManager([]config.MCPConnection{{
		Name: "firecrawl", Transport: "http", Endpoint: httpServer.URL,
		HeaderEnv: map[string]string{"Authorization": "MCP_TEST_TOKEN"}, Timeout: 2 * time.Second,
	}})
	tools, closer, err := manager.OpenTools(context.Background(), []string{"firecrawl"}, func(context.Context, string) error {
		if !allowed.Load() {
			return errors.New("denied")
		}
		return nil
	}, func(context.Context, string) (func(error) error, error) {
		observed.Add(1)
		return func(error) error { observed.Add(1); return nil }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	if len(tools) != 1 || tools[0].Name() != "firecrawl__echo" {
		t.Fatalf("discovered tools = %#v", toolNames(tools))
	}
	result, err := tools[0].(interface {
		Run(agent.ToolContext, any) (map[string]any, error)
	}).Run(testToolContext{Context: context.Background()}, map[string]any{"message": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result["output"] == nil || calls.Load() != 1 || observed.Load() != 2 {
		t.Fatalf("result=%#v calls=%d observations=%d", result, calls.Load(), observed.Load())
	}

	allowed.Store(false)
	_, err = tools[0].(interface {
		Run(agent.ToolContext, any) (map[string]any, error)
	}).Run(testToolContext{Context: context.Background()}, map[string]any{"message": "denied"})
	if err == nil || strings.Contains(err.Error(), "test-secret") || calls.Load() != 1 {
		t.Fatalf("denied dispatch err=%v calls=%d", err, calls.Load())
	}
	if authRequests.Load() == 0 {
		t.Fatal("expected credentialed HTTP requests")
	}
}

func TestGuardedToolDeadlineAndBoundedOutput(t *testing.T) {
	inner := blockingTool{}
	guard := &guardedTool{connection: "slow", inner: inner, name: "slow__wait", timeout: 20 * time.Millisecond}
	_, err := guard.Run(testToolContext{Context: context.Background()}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v", err)
	}
	got := boundResult(map[string]any{"data": strings.Repeat("x", maxToolOutputBytes+100)})
	if len(got) != 1 || len(got["output"].(string)) > maxToolOutputBytes+32 {
		t.Fatalf("oversized output was not bounded: %#v", got)
	}
}

func TestStableToolNameIsBoundedAndSafe(t *testing.T) {
	name := stableToolName("connection", strings.Repeat("unsafe/name-", 12))
	if len(name) > 64 || strings.ContainsAny(name, "/- ") {
		t.Fatalf("invalid stable tool name %q (%d bytes)", name, len(name))
	}
}

func toolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, item := range tools {
		names = append(names, item.Name())
	}
	return names
}

type testToolContext struct {
	agent.ToolContext
	context.Context
}

func (c testToolContext) Deadline() (time.Time, bool)                        { return c.Context.Deadline() }
func (c testToolContext) Done() <-chan struct{}                              { return c.Context.Done() }
func (c testToolContext) Err() error                                         { return c.Context.Err() }
func (c testToolContext) Value(key any) any                                  { return c.Context.Value(key) }
func (testToolContext) ToolConfirmation() *toolconfirmation.ToolConfirmation { return nil }

type blockingTool struct{}

func (blockingTool) Name() string        { return "wait" }
func (blockingTool) Description() string { return "wait" }
func (blockingTool) IsLongRunning() bool { return false }
func (blockingTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{Name: "wait"}
}
func (blockingTool) ProcessRequest(_ agent.ToolContext, req *model.LLMRequest) error {
	return nil
}
func (blockingTool) Run(ctx agent.ToolContext, _ any) (map[string]any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

var _ tool.Tool = blockingTool{}
