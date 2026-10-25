package workflow

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chechiachang/abot/internal/coding"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

type scriptedModel struct {
	mu    sync.Mutex
	calls int
}

func (*scriptedModel) Name() string { return "scripted" }

func (m *scriptedModel) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.mu.Lock()
		m.calls++
		call := m.calls
		m.mu.Unlock()
		var content *genai.Content
		switch call {
		case 1:
			content = &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
				ID: "coding", Name: "coding_agent", Args: map[string]any{"request": "create result.txt"},
			}}}}
		case 2:
			content = &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
				ID: "write", Name: "write", Args: map[string]any{"path": "result.txt", "content": "delegated"},
			}}}}
		case 3:
			content = genai.NewContentFromText("Wrote result.txt.", genai.RoleModel)
		case 4:
			content = genai.NewContentFromText("The coding agent wrote result.txt.", genai.RoleModel)
		default:
			yield(nil, fmt.Errorf("unexpected model call %d", call))
			return
		}
		yield(&model.LLMResponse{Content: content, TurnComplete: true}, nil)
	}
}

func TestRunnerDelegatesRepositoryWriteToCodingAgent(t *testing.T) {
	workdir := t.TempDir()
	tools, err := coding.NewTools(coding.Config{Workdir: workdir, CommandTimeout: time.Second, MaxOutputBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	llm := &scriptedModel{}
	root, err := NewRoot(llm, "system", tools)
	if err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(runner.Config{AppName: "test", Agent: root, SessionService: session.InMemoryService(), AutoCreateSession: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, err := range r.Run(ctx, "user", "session", genai.NewContentFromText("create result.txt", genai.RoleUser), agent.RunConfig{}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(filepath.Join(workdir, "result.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "delegated" {
		t.Fatalf("file content = %q", content)
	}
	llm.mu.Lock()
	defer llm.mu.Unlock()
	if llm.calls != 4 {
		t.Fatalf("model calls = %d, want coordinator→coder→coder→coordinator", llm.calls)
	}
}
