package workflow

import (
	"fmt"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/agenttool"
)

func NewRoot(llm model.LLM, instructions string, tools []tool.Tool) (agent.Agent, error) {
	return NewRootWithCoordinatorTools(llm, instructions, tools, nil)
}

func NewRootWithCoordinatorTools(llm model.LLM, instructions string, codingTools, coordinatorTools []tool.Tool) (agent.Agent, error) {
	coder, err := llmagent.New(llmagent.Config{
		Name:        "coding_agent",
		Description: "Inspects and changes repository files, runs commands, and verifies coding tasks.",
		Model:       llm,
		Instruction: instructions + "\n\nYou are the coding sub-agent. Work only within tools and permissions provided by the host.",
		Tools:       codingTools,
	})
	if err != nil {
		return nil, fmt.Errorf("create coding sub-agent: %w", err)
	}
	coordinatorTools = append([]tool.Tool{agenttool.New(coder, nil)}, coordinatorTools...)
	root, err := llmagent.New(llmagent.Config{
		Name:        "coordinator",
		Description: "Coordinates the conversation and delegates repository implementation tasks to the coding agent.",
		Model:       llm,
		Instruction: instructions + "\n\nYou are the coordinator. Delegate repository inspection or code changes to the coding_agent, then summarize its verified result.",
		Tools:       coordinatorTools,
	})
	if err != nil {
		return nil, fmt.Errorf("create coordinator: %w", err)
	}
	return root, nil
}
