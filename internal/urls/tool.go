package urls

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

type AuthorizeFunc func(context.Context, string) error
type ObserveFunc func(context.Context, string) (func(error) error, error)

func NewTool(reader *Reader, authorize AuthorizeFunc, observe ObserveFunc) (tool.Tool, error) {
	if reader == nil {
		return nil, errors.New("URL reader is required")
	}
	return functiontool.New(functiontool.Config{
		Name:        "read_url_content",
		Description: "Read bounded text from a public HTTP(S) URL. Private, loopback, link-local, reserved, and credentialed targets are rejected.",
	}, func(ctx agent.ToolContext, args struct {
		URL string `json:"url"`
	}) (result Result, err error) {
		if authorize != nil {
			if err := authorize(ctx, "read_url_content"); err != nil {
				return Result{}, fmt.Errorf("tool read_url_content is not allowed by policy")
			}
		}
		var finish func(error) error
		if observe != nil {
			finish, err = observe(ctx, "read_url_content")
			if err != nil {
				return Result{}, err
			}
			if finish != nil {
				defer func() { err = errors.Join(err, finish(err)) }()
			}
		}
		return reader.Read(ctx, args.URL)
	})
}
