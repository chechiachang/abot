package workflow

import (
	"context"
	"iter"
	"slices"
	"testing"

	"google.golang.org/adk/agent/llmagent"
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

func TestNewRootWithModelsAssignsSeparateAgentModels(t *testing.T) {
	coordinatorModel, codingModel := stubModel{}, &namedStubModel{}
	root, err := NewRootWithModels(coordinatorModel, codingModel, "system", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if children := root.SubAgents(); len(children) != 0 {
		t.Fatalf("coordinator exposes sub-agents %v; coding must be tool-only", children)
	}
	base, err := llmagent.New(llmagent.Config{Name: "coding_agent", Model: codingModel})
	if err != nil {
		t.Fatal(err)
	}
	delegation := &codingAgentTool{base: base, models: map[string]model.LLM{codingModel.Name(): codingModel}}
	if delegation.models[codingModel.Name()] != codingModel {
		t.Fatal("coding model not configured on delegation tool")
	}
}

func TestCodingAgentToolPacksItsApprovedModelDeclaration(t *testing.T) {
	base, err := llmagent.New(llmagent.Config{Name: "coding_agent", Model: stubModel{}})
	if err != nil {
		t.Fatal(err)
	}
	delegation := &codingAgentTool{base: base, models: map[string]model.LLM{"approved": stubModel{}}}
	req := &model.LLMRequest{}
	if err := delegation.ProcessRequest(nil, req); err != nil {
		t.Fatal(err)
	}
	if req.Tools[delegation.Name()] != delegation {
		t.Fatal("request did not register the checked delegation wrapper")
	}
	for _, packed := range req.Config.Tools {
		for _, declaration := range packed.FunctionDeclarations {
			if declaration.Name != delegation.Name() {
				continue
			}
			if got := declaration.Parameters.Properties["model"].Enum; len(got) != 1 || !slices.Contains(got, "approved") {
				t.Fatalf("model enum = %v, want [approved]", got)
			}
			return
		}
	}
	t.Fatal("request omitted coding-agent declaration")
}

type namedStubModel struct{}

func (*namedStubModel) Name() string { return "coding-model" }

func (*namedStubModel) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(func(*model.LLMResponse, error) bool) {}
}
