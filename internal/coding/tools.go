package coding

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

type Config struct {
	Workdir        string
	CommandTimeout time.Duration
	MaxOutputBytes int64
	Authorize      func(context.Context, string) error
	Observe        func(context.Context, string) (func(error) error, error)
}

type Tools struct {
	root           string
	commandTimeout time.Duration
	maxOutputBytes int64
	authorize      func(context.Context, string) error
	observe        func(context.Context, string) (func(error) error, error)
}

func NewTools(cfg Config) ([]tool.Tool, error) {
	root, err := filepath.Abs(cfg.Workdir)
	if err != nil {
		return nil, fmt.Errorf("resolve coding workdir: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve coding workdir: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("coding workdir must be an existing directory")
	}
	if cfg.CommandTimeout <= 0 || cfg.MaxOutputBytes <= 0 || cfg.MaxOutputBytes > 10<<20 {
		return nil, errors.New("coding command timeout and output limit must be positive; output is capped at 10 MiB")
	}
	tools := &Tools{root: root, commandTimeout: cfg.CommandTimeout, maxOutputBytes: cfg.MaxOutputBytes, authorize: cfg.Authorize, observe: cfg.Observe}
	readTool, err := functiontool.New(functiontool.Config{Name: "read", Description: "Read a UTF-8 text file under the coding workdir."}, tools.read)
	if err != nil {
		return nil, err
	}
	writeTool, err := functiontool.New(functiontool.Config{Name: "write", Description: "Create or replace a UTF-8 text file under the coding workdir."}, tools.write)
	if err != nil {
		return nil, err
	}
	editTool, err := functiontool.New(functiontool.Config{Name: "edit", Description: "Replace one exact text occurrence in a file under the coding workdir."}, tools.edit)
	if err != nil {
		return nil, err
	}
	bashTool, err := functiontool.New(functiontool.Config{Name: "bash", Description: "Run a shell command in the coding workdir. The workdir is not an OS sandbox."}, tools.bash)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{readTool, writeTool, editTool, bashTool}, nil
}

type fileArgs struct {
	Path string `json:"path"`
}

type readResult struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

func (t *Tools) read(ctx agent.ToolContext, args fileArgs) (result readResult, err error) {
	finish, err := t.begin(ctx, "read")
	if err != nil {
		return readResult{}, err
	}
	defer func() { err = errors.Join(err, finish(err)) }()
	if ctx != nil && ctx.Err() != nil {
		return readResult{}, ctx.Err()
	}
	path, err := t.resolve(args.Path, false)
	if err != nil {
		return readResult{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return readResult{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, t.maxOutputBytes+1))
	if err != nil {
		return readResult{}, err
	}
	truncated := int64(len(data)) > t.maxOutputBytes
	if truncated {
		data = data[:t.maxOutputBytes]
	}
	return readResult{Path: args.Path, Content: string(data), Truncated: truncated}, nil
}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type writeResult struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytes_written"`
}

func (t *Tools) write(ctx agent.ToolContext, args writeArgs) (result writeResult, err error) {
	finish, err := t.begin(ctx, "write")
	if err != nil {
		return writeResult{}, err
	}
	defer func() { err = errors.Join(err, finish(err)) }()
	if ctx != nil && ctx.Err() != nil {
		return writeResult{}, ctx.Err()
	}
	if int64(len(args.Content)) > t.maxOutputBytes {
		return writeResult{}, fmt.Errorf("file content exceeds %d byte limit", t.maxOutputBytes)
	}
	path, err := t.resolve(args.Path, true)
	if err != nil {
		return writeResult{}, err
	}
	mode := os.FileMode(0644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(args.Content), mode); err != nil {
		return writeResult{}, err
	}
	return writeResult{Path: args.Path, BytesWritten: len(args.Content)}, nil
}

type editArgs struct {
	Path    string `json:"path"`
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

func (t *Tools) edit(ctx agent.ToolContext, args editArgs) (result writeResult, err error) {
	finish, err := t.begin(ctx, "edit")
	if err != nil {
		return writeResult{}, err
	}
	defer func() { err = errors.Join(err, finish(err)) }()
	return t.editWithContext(ctx, args)
}

func (t *Tools) editWithContext(ctx context.Context, args editArgs) (writeResult, error) {
	if args.OldText == "" {
		return writeResult{}, errors.New("old_text must not be empty")
	}
	path, err := t.resolve(args.Path, false)
	if err != nil {
		return writeResult{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return writeResult{}, err
	}
	if int64(len(data)) > t.maxOutputBytes {
		return writeResult{}, fmt.Errorf("file exceeds %d byte limit", t.maxOutputBytes)
	}
	if count := strings.Count(string(data), args.OldText); count != 1 {
		return writeResult{}, fmt.Errorf("expected one exact match, found %d", count)
	}
	updated := strings.Replace(string(data), args.OldText, args.NewText, 1)
	if int64(len(updated)) > t.maxOutputBytes {
		return writeResult{}, fmt.Errorf("edited file exceeds %d byte limit", t.maxOutputBytes)
	}
	if err := ctx.Err(); err != nil {
		return writeResult{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return writeResult{}, err
	}
	if err := os.WriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return writeResult{}, err
	}
	return writeResult{Path: args.Path, BytesWritten: len(updated)}, nil
}

type bashArgs struct {
	Command string `json:"command"`
}

type bashResult struct {
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
}

func (t *Tools) bash(ctx agent.ToolContext, args bashArgs) (result bashResult, err error) {
	finish, err := t.begin(ctx, "bash")
	if err != nil {
		return bashResult{}, err
	}
	defer func() { err = errors.Join(err, finish(err)) }()
	return t.runCommand(ctx, args)
}

func (t *Tools) begin(ctx context.Context, name string) (func(error) error, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if t.authorize != nil {
		if err := t.authorize(ctx, name); err != nil {
			return nil, fmt.Errorf("tool %q is not allowed: %w", name, err)
		}
	}
	if t.observe != nil {
		finish, err := t.observe(ctx, name)
		if err != nil {
			return nil, err
		}
		if finish != nil {
			return finish, nil
		}
	}
	return func(error) error { return nil }, nil
}

func (t *Tools) runCommand(ctx context.Context, args bashArgs) (bashResult, error) {
	execCtx, cancel := context.WithTimeout(ctx, t.commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(execCtx, "/bin/sh", "-lc", args.Command)
	cmd.Dir = t.root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	output := &cappedBuffer{limit: t.maxOutputBytes}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	if execCtx.Err() != nil {
		return bashResult{}, execCtx.Err()
	}
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = -1
			output.Write([]byte(err.Error()))
		}
	}
	return bashResult{ExitCode: code, Output: output.String(), Truncated: output.truncated}, nil
}

func (t *Tools) resolve(name string, allowMissing bool) (string, error) {
	if strings.TrimSpace(name) == "" || filepath.IsAbs(name) {
		return "", errors.New("path must be relative to the coding workdir")
	}
	candidate := filepath.Clean(filepath.Join(t.root, filepath.FromSlash(name)))
	if !within(t.root, candidate) {
		return "", errors.New("path escapes the coding workdir")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err == nil {
		if !within(t.root, resolved) {
			return "", errors.New("path escapes the coding workdir through a symlink")
		}
		return resolved, nil
	}
	if !allowMissing || !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(candidate))
	if err != nil {
		return "", err
	}
	if !within(t.root, parent) {
		return "", errors.New("path escapes the coding workdir through a symlink")
	}
	return filepath.Join(parent, filepath.Base(candidate)), nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type cappedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int64
	truncated bool
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - int64(b.buffer.Len())
	if remaining > 0 {
		n := len(data)
		if int64(n) > remaining {
			n = int(remaining)
			b.truncated = true
		}
		_, _ = b.buffer.Write(data[:n])
	} else if len(data) > 0 {
		b.truncated = true
	}
	return len(data), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
