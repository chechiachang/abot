package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/chechiachang/abot/internal/coding"
	"github.com/chechiachang/abot/internal/config"
	"github.com/chechiachang/abot/internal/instructions"
	mcpconnections "github.com/chechiachang/abot/internal/integrations/mcp"
	"github.com/chechiachang/abot/internal/media"
	"github.com/chechiachang/abot/internal/policy"
	"github.com/chechiachang/abot/internal/provider/azureopenai"
	agentruntime "github.com/chechiachang/abot/internal/runtime"
	"github.com/chechiachang/abot/internal/skills"
	"github.com/chechiachang/abot/internal/urls"
	"github.com/chechiachang/abot/internal/workflow"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

type PolicyLoader func() (policy.Policy, error)

type Engine struct {
	Config     config.Config
	State      *agentruntime.Store
	Turns      *agentruntime.TurnManager
	Media      *media.Store
	LoadPolicy PolicyLoader
	Logger     *logrus.Logger
}

type Result struct {
	RequestID string
	EventID   string
	Text      string
	Progress  []agentruntime.Step
}

func (e *Engine) Run(parent context.Context, chatID string, numericChatID int64, userID, branchID, prompt string, onEvent func(workflow.Event)) (Result, error) {
	return e.RunWithMedia(parent, chatID, numericChatID, userID, branchID, prompt, nil, onEvent)
}

func (e *Engine) RunWithMedia(parent context.Context, chatID string, numericChatID int64, userID, branchID, prompt string, mediaParts []*genai.Part, onEvent func(workflow.Event)) (Result, error) {
	ctx, span := otel.Tracer("abot/engine").Start(parent, "agent.turn", trace.WithAttributes(
		attribute.String("chat.id", chatID), attribute.String("branch.id", branchID),
	))
	defer span.End()
	result, err := e.runWithMedia(ctx, chatID, numericChatID, userID, branchID, prompt, mediaParts, onEvent)
	if err != nil {
		span.SetStatus(codes.Error, "agent turn failed")
		span.SetAttributes(attribute.String("error.type", fmt.Sprintf("%T", err)))
	}
	if result.RequestID != "" {
		span.SetAttributes(attribute.String("request.id", result.RequestID))
	}
	return result, err
}

func (e *Engine) runWithMedia(parent context.Context, chatID string, numericChatID int64, userID, branchID, prompt string, mediaParts []*genai.Part, onEvent func(workflow.Event)) (Result, error) {
	if e == nil || e.State == nil {
		return Result{}, errors.New("agent engine requires a durable runtime store")
	}
	if strings.TrimSpace(chatID) == "" || strings.TrimSpace(userID) == "" || strings.TrimSpace(branchID) == "" || strings.TrimSpace(prompt) == "" {
		return Result{}, errors.New("chat, user, branch, and prompt are required")
	}
	if e.Turns == nil {
		e.Turns = agentruntime.NewTurnManager()
	}
	turnCtx, finishTurnContext, err := e.Turns.Start(parent, chatID, branchID)
	if err != nil {
		return Result{}, err
	}
	defer finishTurnContext()
	turnCtx, cancel := context.WithTimeout(turnCtx, e.Config.Runtime.TurnTimeout)
	defer cancel()

	currentPolicy, err := e.policy()
	if err != nil {
		return Result{}, err
	}
	selection, err := currentPolicy.Effective(numericChatID)
	if err != nil {
		return Result{}, err
	}
	instruction, err := instructions.Load(e.Config.Instructions.SystemFile, e.Config.Instructions.SoulFile)
	if err != nil {
		return Result{}, err
	}
	catalog, err := skills.Discover(e.Config.Skills.Paths)
	if err != nil {
		return Result{}, err
	}
	selectedSkills, err := catalog.LoadSelected(selection.Skills)
	if err != nil {
		return Result{}, err
	}
	instruction += "\n\n" + skills.Render(selectedSkills)
	llm, err := azureopenai.NewModel(azureopenai.Config{
		BaseURL: e.Config.Azure.BaseURL, APIKey: e.Config.Azure.APIKey, Model: selection.Model,
		ReasoningEffort: selection.ThinkingLevel, MaxRetries: e.Config.Azure.MaxRetries, Timeout: e.Config.Azure.Timeout,
	})
	if err != nil {
		return Result{}, err
	}
	requestID, err := workflow.NewRequestID()
	if err != nil {
		return Result{}, fmt.Errorf("create request ID: %w", err)
	}
	ids := workflow.IDs{RequestID: requestID, ChatID: chatID}
	branch, err := e.State.EnsureBranch(turnCtx, chatID, branchID)
	if err != nil {
		return Result{}, err
	}
	if err := e.State.BeginTurn(turnCtx, chatID, branchID, requestID); err != nil {
		return Result{}, err
	}
	failed := func(kind string, err error, eventID string) (Result, error) {
		status := agentruntime.TaskFailed
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(turnCtx.Err(), context.Canceled) {
			status = agentruntime.TaskCancelled
		}
		if finishErr := e.State.FinishTurn(context.Background(), requestID, status, eventID, kind); finishErr != nil {
			err = errors.Join(err, finishErr)
		}
		return Result{RequestID: requestID, EventID: eventID}, err
	}

	authorize := func(ctx context.Context, name string) error {
		activePolicy, err := e.policy()
		if err != nil {
			return err
		}
		effective, err := activePolicy.Effective(numericChatID)
		if err != nil {
			return err
		}
		for _, allowed := range effective.Tools {
			if name == allowed {
				return ctx.Err()
			}
		}
		return fmt.Errorf("tool %q is not in the effective policy", name)
	}
	tools, err := coding.NewTools(coding.Config{
		Workdir: e.Config.Coding.Workdir, CommandTimeout: e.Config.Coding.CommandTimeout,
		MaxOutputBytes: e.Config.Coding.MaxOutputBytes, Authorize: authorize, Observe: e.State.ToolObserver(requestID),
	})
	if err != nil {
		return failed("tool_setup_error", err, "")
	}
	tools = filterTools(tools, selection.Tools)
	authorizeMCP := func(ctx context.Context, connectionName string) error {
		activePolicy, err := e.policy()
		if err != nil {
			return err
		}
		effective, err := activePolicy.Effective(numericChatID)
		if err != nil {
			return err
		}
		for _, allowed := range effective.MCP {
			if connectionName == allowed {
				return ctx.Err()
			}
		}
		return fmt.Errorf("MCP connection %q is not in the effective policy", connectionName)
	}
	mcpTools, mcpCloser, err := mcpconnections.NewManager(e.Config.MCP.Connections).OpenTools(turnCtx, selection.MCP, authorizeMCP, e.State.ToolObserver(requestID))
	if err != nil {
		return failed("mcp_setup_error", err, "")
	}
	defer mcpCloser.Close()
	coordinatorTools := append([]tool.Tool(nil), mcpTools...)
	if contains(selection.Tools, "read_url_content") {
		mediaStore := e.Media
		if mediaStore == nil {
			mediaStore, err = media.NewStore(e.Config.Media.Root, media.Limits{
				MaxBytes: e.Config.Media.MaxBytes, MaxDuration: e.Config.Media.MaxDuration, MaxPages: e.Config.Media.MaxPages,
				MaxExtractBytes: e.Config.Media.MaxExtractBytes, Pandoc: e.Config.Media.Pandoc, PDFInfo: e.Config.Media.PDFInfo,
				PDFToText: e.Config.Media.PDFToText, FFmpeg: e.Config.Media.FFmpeg, FFprobe: e.Config.Media.FFprobe,
				Whisper: e.Config.Media.Whisper, WhisperModel: e.Config.Media.WhisperModel,
			})
			if err != nil {
				return failed("media_setup_error", err, "")
			}
		}
		urlReader, err := urls.NewReader(urls.Config{
			Timeout: e.Config.URLReader.Timeout, MaxBytes: e.Config.URLReader.MaxBytes,
			MaxOutputBytes: e.Config.URLReader.MaxOutputBytes, MaxRedirects: e.Config.URLReader.MaxRedirects, MaxAudioDuration: e.Config.Media.MaxDuration,
			FirecrawlEndpoint: e.Config.URLReader.FirecrawlEndpoint, FirecrawlAPIKey: e.Config.URLReader.FirecrawlAPIKey,
			FirecrawlExternalTransfer: e.Config.URLReader.FirecrawlExternalTransfer, BrowserExecutable: e.Config.URLReader.BrowserExecutable,
			YouTubeDL: e.Config.Media.YouTubeDL,
		}, mediaStore)
		if err != nil {
			return failed("url_reader_setup_error", err, "")
		}
		urlTool, err := urls.NewTool(urlReader, authorize, e.State.ToolObserver(requestID))
		if err != nil {
			return failed("url_tool_setup_error", err, "")
		}
		coordinatorTools = append(coordinatorTools, urlTool)
	}
	root, err := workflow.NewRootWithCoordinatorTools(llm, instruction, tools, coordinatorTools)
	if err != nil {
		return failed("agent_setup_error", err, "")
	}
	r, err := runner.New(runner.Config{AppName: "abot", Agent: root, SessionService: e.State.Sessions(), AutoCreateSession: true})
	if err != nil {
		return failed("runner_error", err, "")
	}
	var answer strings.Builder
	partial := false
	resultEventID := ""
	progress := make([]agentruntime.Step, 0)
	input := genai.NewContentFromText(prompt, genai.RoleUser)
	input.Parts = append(input.Parts, mediaParts...)
	for event, err := range r.Run(turnCtx, userID, branch.SessionID, input, agent.RunConfig{StreamingMode: agent.StreamingModeSSE}) {
		if err != nil {
			return failed(errorKind(err), err, resultEventID)
		}
		if event == nil {
			continue
		}
		update := workflow.FromADK(ids, root.Name(), event)
		if update.Kind == workflow.EventResult {
			resultEventID = event.ID
			if err := e.State.UpdateCheckpoint(turnCtx, chatID, branchID, event.ID); err != nil {
				return failed("checkpoint_error", err, resultEventID)
			}
		}
		if update.Tool != "" && update.ToolState != "" {
			if update.ToolState == "started" {
				progress = append(progress, agentruntime.Step{ID: fmt.Sprintf("tool-%d", len(progress)+1), Title: "Run " + update.Tool, Status: agentruntime.StepInProgress})
			} else {
				for i := len(progress) - 1; i >= 0; i-- {
					if progress[i].Title == "Run "+update.Tool && progress[i].Status == agentruntime.StepInProgress {
						progress[i].Status = agentruntime.StepCompleted
						break
					}
				}
			}
			if err := e.State.SetProgress(turnCtx, chatID, branchID, requestID, progress); err != nil {
				return failed("progress_store_error", err, resultEventID)
			}
		}
		if update.Agent == root.Name() && update.Text != "" {
			if update.Partial {
				answer.WriteString(update.Text)
				partial = true
			} else if update.Kind == workflow.EventResult && !partial {
				answer.WriteString(update.Text)
			}
		}
		if onEvent != nil {
			onEvent(update)
		}
	}
	if answer.Len() == 0 {
		return failed("empty_result", errors.New("agent completed without a text response"), resultEventID)
	}
	if err := e.State.FinishTurn(context.Background(), requestID, agentruntime.TaskCompleted, resultEventID, ""); err != nil {
		return Result{RequestID: requestID, EventID: resultEventID, Text: answer.String()}, err
	}
	return Result{RequestID: requestID, EventID: resultEventID, Text: answer.String(), Progress: progress}, nil
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func (e *Engine) policy() (policy.Policy, error) {
	if e.LoadPolicy != nil {
		return e.LoadPolicy()
	}
	return e.Config.EffectivePolicy(), nil
}

func filterTools(tools []tool.Tool, allowed []string) []tool.Tool {
	filtered := make([]tool.Tool, 0, len(tools))
	for _, item := range tools {
		for _, name := range allowed {
			if item.Name() == name {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

func errorKind(err error) string {
	var providerErr *azureopenai.ProviderError
	if errors.As(err, &providerErr) {
		return string(providerErr.Kind)
	}
	var configErr *config.Error
	if errors.As(err, &configErr) {
		return "configuration"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	return "agent_error"
}
