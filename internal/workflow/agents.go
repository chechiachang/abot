package workflow

import (
	"fmt"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/agenttool"
	"google.golang.org/genai"
	"slices"
)

func NewRoot(llm model.LLM, instructions string, tools []tool.Tool) (agent.Agent, error) {
	return NewRootWithCoordinatorTools(llm, instructions, tools, nil)
}
func NewRootWithCoordinatorTools(llm model.LLM, instructions string, codingTools, coordinatorTools []tool.Tool) (agent.Agent, error) {
	return NewRootWithModels(llm, llm, instructions, codingTools, coordinatorTools)
}
func NewRootWithModels(coordinatorModel, codingModel model.LLM, instructions string, codingTools, coordinatorTools []tool.Tool) (agent.Agent, error) {
	return NewRootWithApprovedModels(coordinatorModel, codingModel, nil, instructions, codingTools, coordinatorTools)
}
func NewRootWithApprovedModels(coordinatorModel, codingModel model.LLM, approved map[string]model.LLM, instructions string, codingTools, coordinatorTools []tool.Tool) (agent.Agent, error) {
	models := make(map[string]model.LLM, len(approved)+1)
	for n, m := range approved {
		models[n] = m
	}
	if codingModel != nil {
		models[codingModel.Name()] = codingModel
	}
	coder, err := llmagent.New(llmagent.Config{Name: "coding_agent", Description: "Inspects and changes repository files, runs commands, and verifies coding tasks.", Model: codingModel, Instruction: instructions + "\n\nYou are the coding sub-agent. Work only within tools and permissions provided by the host.", Tools: codingTools})
	if err != nil {
		return nil, fmt.Errorf("create coding sub-agent: %w", err)
	}
	delegation := &codingAgentTool{base: coder, instruction: instructions, tools: codingTools, models: models}
	coordinatorTools = append([]tool.Tool{delegation}, coordinatorTools...)
	root, err := llmagent.New(llmagent.Config{Name: "coordinator", Description: "Coordinates conversation and delegates repository work to the coding agent.", Model: coordinatorModel, Instruction: instructions + "\n\nYou are the coordinator. Delegate repository work using coding_agent. Optionally specify an approved model deployment from the tool schema; omit it to use the turn model. Summarize verified results.", SubAgents: []agent.Agent{coder}, Tools: coordinatorTools})
	if err != nil {
		return nil, fmt.Errorf("create coordinator: %w", err)
	}
	return root, nil
}

type codingAgentTool struct {
	base        agent.Agent
	instruction string
	tools       []tool.Tool
	models      map[string]model.LLM
}

func (t *codingAgentTool) Name() string { return t.base.Name() }
func (t *codingAgentTool) Description() string {
	return "Delegate repository work; optionally supply an approved coding model deployment."
}
func (t *codingAgentTool) IsLongRunning() bool { return false }
func (t *codingAgentTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{Name: t.Name(), Description: t.Description(), Parameters: &genai.Schema{Type: genai.TypeObject, Properties: map[string]*genai.Schema{"request": {Type: genai.TypeString}, "model": {Type: genai.TypeString, Enum: modelNames(t.models)}}, Required: []string{"request"}}}
}
func (t *codingAgentTool) ProcessRequest(ctx agent.ToolContext, r *model.LLMRequest) error {
	p, ok := agenttool.New(t.base, nil).(interface {
		ProcessRequest(agent.ToolContext, *model.LLMRequest) error
	})
	if ok {
		return p.ProcessRequest(ctx, r)
	}
	return nil
}
func (t *codingAgentTool) Run(ctx agent.ToolContext, args any) (map[string]any, error) {
	v, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("coding agent expects object arguments, got %T", args)
	}
	request, ok := v["request"].(string)
	if !ok || request == "" {
		return nil, fmt.Errorf("coding agent request is required")
	}
	target := t.base
	if raw, exists := v["model"]; exists {
		name, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("coding agent model must be a string")
		}
		m := t.models[name]
		if m == nil {
			return nil, fmt.Errorf("coding agent model %q is not approved", name)
		}
		var err error
		target, err = llmagent.New(llmagent.Config{Name: t.base.Name(), Description: t.base.Description(), Model: m, Instruction: t.instruction + "\n\nYou are the coding sub-agent. Work only within tools and permissions provided by the host.", Tools: t.tools})
		if err != nil {
			return nil, fmt.Errorf("create selected coding agent: %w", err)
		}
	}
	runner, ok := agenttool.New(target, nil).(interface {
		Run(agent.ToolContext, any) (map[string]any, error)
	})
	if !ok {
		return nil, fmt.Errorf("coding delegation unavailable")
	}
	return runner.Run(ctx, map[string]any{"request": request})
}
func (t *codingAgentTool) DisplayResultOnSkipSummarization() bool { return true }
func modelNames(ms map[string]model.LLM) []string {
	ns := make([]string, 0, len(ms))
	for n := range ms {
		ns = append(ns, n)
	}
	slices.Sort(ns)
	return ns
}
