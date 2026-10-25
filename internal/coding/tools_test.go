package coding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileToolsStayWithinWorkdir(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	toolset := &Tools{root: root, maxOutputBytes: 32}
	if _, err := toolset.write(nil, writeArgs{Path: "notes.txt", Content: "before"}); err != nil {
		t.Fatal(err)
	}
	if _, err := toolset.editWithContext(context.Background(), editArgs{Path: "notes.txt", OldText: "before", NewText: "after"}); err != nil {
		t.Fatal(err)
	}
	result, err := toolset.read(nil, fileArgs{Path: "notes.txt"})
	if err != nil || result.Content != "after" {
		t.Fatalf("read result=%#v, err=%v", result, err)
	}
	if _, err := toolset.read(nil, fileArgs{Path: "../outside.txt"}); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := toolset.read(nil, fileArgs{Path: "escape"}); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestBashBoundsOutputAndHonorsCancellation(t *testing.T) {
	toolset := &Tools{root: t.TempDir(), commandTimeout: time.Second, maxOutputBytes: 4}
	result, err := toolset.runCommand(context.Background(), bashArgs{Command: "printf 123456"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Output != "1234" || !result.Truncated {
		t.Fatalf("command result = %#v", result)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	toolset.commandTimeout = time.Second
	_, err = toolset.runCommand(ctx, bashArgs{Command: "sleep 5"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestToolAuthorizationRunsBeforeSideEffectsAndObserverRecordsExecution(t *testing.T) {
	workdir := t.TempDir()
	var started, finished int
	root, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		t.Fatal(err)
	}
	tools := &Tools{
		root: root, commandTimeout: time.Second, maxOutputBytes: 1024,
		authorize: func(context.Context, string) error { return errors.New("policy denied") },
		observe: func(context.Context, string) (func(error) error, error) {
			started++
			return func(error) error { finished++; return nil }, nil
		},
	}
	if _, err := tools.write(nil, writeArgs{Path: "denied.txt", Content: "no"}); err == nil {
		t.Fatal("expected denied tool call")
	}
	if _, err := os.Stat(filepath.Join(workdir, "denied.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("denied write created a file: %v", err)
	}
	if started != 0 || finished != 0 {
		t.Fatalf("denied tool was recorded: started=%d finished=%d", started, finished)
	}

	tools.authorize = nil
	if _, err := tools.write(nil, writeArgs{Path: "allowed.txt", Content: "yes"}); err != nil {
		t.Fatal(err)
	}
	if started != 1 || finished != 1 {
		t.Fatalf("observer calls = started:%d finished:%d", started, finished)
	}
}

func TestEditRequiresOneExactMatch(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "duplicate.txt")
	if err := os.WriteFile(path, []byte("same same"), 0600); err != nil {
		t.Fatal(err)
	}
	toolset := &Tools{root: root, maxOutputBytes: 32}
	_, err = toolset.editWithContext(context.Background(), editArgs{Path: "duplicate.txt", OldText: "same", NewText: "changed"})
	if err == nil || !strings.Contains(err.Error(), "found 2") {
		t.Fatalf("duplicate edit error = %v", err)
	}
}
