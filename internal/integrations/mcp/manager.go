package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/chechiachang/abot/internal/config"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/mcptoolset"
	"google.golang.org/genai"
)

const maxToolOutputBytes = 1 << 20

type AuthorizeFunc func(context.Context, string) error
type ObserveFunc func(context.Context, string) (func(error) error, error)

type Manager struct {
	connections []config.MCPConnection
}

func NewManager(connections []config.MCPConnection) *Manager {
	return &Manager{connections: append([]config.MCPConnection(nil), connections...)}
}

func (m *Manager) OpenTools(ctx context.Context, selected []string, authorize AuthorizeFunc, observe ObserveFunc) ([]tool.Tool, io.Closer, error) {
	configured := make(map[string]config.MCPConnection, len(m.connections))
	for _, connection := range m.connections {
		configured[connection.Name] = connection
	}
	var tools []tool.Tool
	var closers []io.Closer
	closeAll := func() { _ = (&closerGroup{closers: closers}).Close() }
	for _, name := range selected {
		connection, exists := configured[name]
		if !exists {
			closeAll()
			return nil, nil, fmt.Errorf("selected MCP connection %q is not configured", name)
		}
		var available []tool.Tool
		var setCloser io.Closer
		var lastErr error
		for attempt := 0; attempt <= connection.MaxRetries; attempt++ {
			transport, err := newTransport(connection)
			if err != nil {
				lastErr = err
				break
			}
			set, err := mcptoolset.New(mcptoolset.Config{Transport: transport})
			if err != nil {
				lastErr = err
				break
			}
			setCloser, _ = set.(io.Closer)
			probeCtx, cancel := context.WithTimeout(ctx, connectionTimeout(connection))
			available, lastErr = set.Tools(&readonlyContext{Context: probeCtx})
			cancel()
			if lastErr == nil {
				break
			}
			if setCloser != nil {
				_ = setCloser.Close()
				setCloser = nil
			}
			if attempt < connection.MaxRetries {
				delay := time.Duration(1<<min(attempt, 5)) * 100 * time.Millisecond
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					lastErr = ctx.Err()
					attempt = connection.MaxRetries
				case <-timer.C:
				}
			}
		}
		if lastErr != nil {
			closeAll()
			if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
				return nil, nil, lastErr
			}
			return nil, nil, fmt.Errorf("MCP connection %q could not list tools after %d attempts; check its endpoint, command, and credentials", name, connection.MaxRetries+1)
		}
		if setCloser != nil {
			closers = append(closers, setCloser)
		}
		for _, candidate := range available {
			runnable, ok := candidate.(runnableTool)
			if !ok {
				continue
			}
			qualifiedName := stableToolName(name, candidate.Name())
			for _, existing := range tools {
				if existing.Name() == qualifiedName {
					closeAll()
					return nil, nil, fmt.Errorf("MCP servers expose colliding tool name %q", qualifiedName)
				}
			}
			tools = append(tools, &guardedTool{
				connection: name, inner: runnable, name: qualifiedName, timeout: connectionTimeout(connection),
				authorize: authorize, observe: observe,
			})
		}
	}
	return tools, &closerGroup{closers: closers}, nil
}

func (m *Manager) Probe(ctx context.Context, name string) ([]tool.Tool, io.Closer, error) {
	return m.OpenTools(ctx, []string{name}, nil, nil)
}

func stableToolName(connection, toolName string) string {
	return sanitizeToolName(connection + "__" + toolName)
}

func sanitizeToolName(name string) string {
	var result strings.Builder
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' {
			result.WriteRune(char)
		} else {
			result.WriteByte('_')
		}
	}
	name = result.String()
	if name == "" {
		name = "tool"
	}
	if name[0] >= '0' && name[0] <= '9' {
		name = "mcp_" + name
	}
	if len(name) > 64 {
		hash := sha256.Sum256([]byte(name))
		name = name[:55] + "_" + fmt.Sprintf("%08x", hash[:4])
	}
	return name
}

func newTransport(connection config.MCPConnection) (sdk.Transport, error) {
	timeout := connectionTimeout(connection)
	if connection.Transport == "stdio" {
		command := exec.Command(connection.Command, connection.Args...)
		environment := make(map[string]string)
		for _, name := range append([]string{"PATH"}, connection.Environment...) {
			if value, ok := os.LookupEnv(name); ok {
				environment[name] = value
			} else if name != "PATH" {
				return nil, fmt.Errorf("environment variable %s is not set", name)
			}
		}
		for key, value := range environment {
			command.Env = append(command.Env, key+"="+value)
		}
		return &sdk.CommandTransport{Command: command, TerminateDuration: 2 * time.Second}, nil
	}
	if connection.Transport != "http" {
		return nil, errors.New("transport must be stdio or http")
	}
	headers := make(http.Header)
	for header, environment := range connection.HeaderEnv {
		value, ok := os.LookupEnv(environment)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("environment variable %s for HTTP header %s is not set", environment, header)
		}
		headers.Set(header, value)
	}
	client := &http.Client{Timeout: timeout, Transport: headerRoundTripper{base: http.DefaultTransport, headers: headers}}
	retries := connection.MaxRetries
	if retries == 0 {
		retries = -1
	}
	return &sdk.StreamableClientTransport{Endpoint: connection.Endpoint, HTTPClient: client, MaxRetries: retries}, nil
}

func connectionTimeout(connection config.MCPConnection) time.Duration {
	if connection.Timeout <= 0 {
		return 30 * time.Second
	}
	return connection.Timeout
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers http.Header
}

func (t headerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	for name, values := range t.headers {
		clone.Header.Del(name)
		for _, value := range values {
			clone.Header.Add(name, value)
		}
	}
	return t.base.RoundTrip(clone)
}

type closerGroup struct{ closers []io.Closer }

func (g *closerGroup) Close() error {
	var errs []error
	for i := len(g.closers) - 1; i >= 0; i-- {
		if err := g.closers[i].Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type runnableTool interface {
	tool.Tool
	Declaration() *genai.FunctionDeclaration
	Run(agent.ToolContext, any) (map[string]any, error)
}

type guardedTool struct {
	connection string
	inner      runnableTool
	name       string
	timeout    time.Duration
	authorize  AuthorizeFunc
	observe    ObserveFunc
}

func (t *guardedTool) Name() string { return t.name }
func (t *guardedTool) Description() string {
	return "MCP " + t.connection + ": " + t.inner.Description()
}
func (t *guardedTool) IsLongRunning() bool { return t.inner.IsLongRunning() }

func (t *guardedTool) Declaration() *genai.FunctionDeclaration {
	declaration := *t.inner.Declaration()
	declaration.Name = t.name
	return &declaration
}

func (t *guardedTool) ProcessRequest(_ agent.ToolContext, request *model.LLMRequest) error {
	if request.Tools == nil {
		request.Tools = make(map[string]any)
	}
	if _, exists := request.Tools[t.name]; exists {
		return fmt.Errorf("duplicate MCP tool %q", t.name)
	}
	request.Tools[t.name] = t
	if request.Config == nil {
		request.Config = &genai.GenerateContentConfig{}
	}
	var declarations *genai.Tool
	for _, candidate := range request.Config.Tools {
		if candidate != nil && candidate.FunctionDeclarations != nil {
			declarations = candidate
			break
		}
	}
	if declarations == nil {
		declarations = &genai.Tool{}
		request.Config.Tools = append(request.Config.Tools, declarations)
	}
	declarations.FunctionDeclarations = append(declarations.FunctionDeclarations, t.Declaration())
	return nil
}

func (t *guardedTool) Run(ctx agent.ToolContext, args any) (result map[string]any, err error) {
	if t.authorize != nil {
		if err := t.authorize(ctx, t.connection); err != nil {
			return nil, fmt.Errorf("MCP connection %q is no longer allowed", t.connection)
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	boundedCtx := &boundedToolContext{ToolContext: ctx, Context: callCtx}
	var finish func(error) error
	if t.observe != nil {
		finish, err = t.observe(boundedCtx, t.name)
		if err != nil {
			return nil, err
		}
		if finish != nil {
			defer func() { err = errors.Join(err, finish(err)) }()
		}
	}
	result, err = t.inner.Run(boundedCtx, args)
	if err != nil {
		if callCtx.Err() != nil {
			return nil, callCtx.Err()
		}
		return nil, fmt.Errorf("MCP tool %q failed; check the server and its credentials", t.name)
	}
	return boundResult(result), nil
}

func boundResult(result map[string]any) map[string]any {
	encoded, err := json.Marshal(result)
	if err != nil {
		return map[string]any{"error": "tool output could not be encoded"}
	}
	if len(encoded) <= maxToolOutputBytes {
		return result
	}
	return map[string]any{"output": strings.ToValidUTF8(string(encoded[:maxToolOutputBytes]), "") + "\n[truncated]"}
}

type boundedToolContext struct {
	agent.ToolContext
	context.Context
}

func (c *boundedToolContext) Deadline() (time.Time, bool) { return c.Context.Deadline() }
func (c *boundedToolContext) Done() <-chan struct{}       { return c.Context.Done() }
func (c *boundedToolContext) Err() error                  { return c.Context.Err() }
func (c *boundedToolContext) Value(key any) any           { return c.Context.Value(key) }

type readonlyContext struct {
	context.Context
}

func (*readonlyContext) UserContent() *genai.Content          { return nil }
func (*readonlyContext) InvocationID() string                 { return "mcp-discovery" }
func (*readonlyContext) AgentName() string                    { return "coordinator" }
func (*readonlyContext) ReadonlyState() session.ReadonlyState { return nil }
func (*readonlyContext) UserID() string                       { return "mcp-admin" }
func (*readonlyContext) AppName() string                      { return "abot" }
func (*readonlyContext) SessionID() string                    { return "mcp-discovery" }
func (*readonlyContext) Branch() string                       { return "main" }

var _ tool.Tool = (*guardedTool)(nil)
