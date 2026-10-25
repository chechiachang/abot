package workflow

import (
	"context"
	"iter"
	"testing"

	"google.golang.org/adk/model"
)

type stubModel struct{}

func (stubModel) Name() string { return "stub" }

func (stubModel) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(func(*model.LLMResponse, error) bool) {}
}

func TestNewRootCreatesCoordinator(t *testing.T) {
	root, err := NewRoot(stubModel{}, "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if root.Name() != "coordinator" {
		t.Fatalf("root name = %q, want coordinator", root.Name())
	}
}
