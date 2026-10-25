package urls

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool/toolconfirmation"
)

func TestURLToolRechecksPolicyAtEveryDirectDispatch(t *testing.T) {
	reader := testReader(t, mapResolver{"public.test": {netip.MustParseAddr("93.184.216.34")}})
	var requests, authorizations, observations int
	reader.fetch = func(context.Context, string, string, http.Header, []byte) (response, error) {
		requests++
		return response{status: http.StatusOK, contentType: "text/plain", body: []byte("source text")}, nil
	}
	allowed := false
	item, err := NewTool(reader, func(context.Context, string) error {
		authorizations++
		if !allowed {
			return errors.New("denied")
		}
		return nil
	}, func(context.Context, string) (func(error) error, error) {
		observations++
		return func(error) error { return nil }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.Name() != "read_url_content" {
		t.Fatalf("tool name=%q", item.Name())
	}
	runner := item.(interface {
		Run(agent.ToolContext, any) (map[string]any, error)
	})
	ctx := testToolContext{Context: context.Background()}
	if _, err := runner.Run(ctx, map[string]any{"url": "https://public.test/"}); err == nil || requests != 0 {
		t.Fatalf("denied URL tool err=%v requests=%d", err, requests)
	}
	allowed = true
	if _, err := runner.Run(ctx, map[string]any{"url": "https://public.test/"}); err != nil {
		t.Fatal(err)
	}
	if authorizations != 2 || observations != 1 || requests != 1 {
		t.Fatalf("dispatches auth=%d observe=%d requests=%d", authorizations, observations, requests)
	}
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
