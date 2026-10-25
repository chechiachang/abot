package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"

	"github.com/chechiachang/abot/internal/cli"
	"github.com/chechiachang/abot/internal/logging"
	"github.com/joho/godotenv"
)

func main() {
	if err := loadDotEnv(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "could not load .env")
		os.Exit(1)
	}
	cmd := cli.NewRootCmd()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd.SetContext(ctx)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, logging.Redact(err.Error()))
		os.Exit(1)
	}
}

func loadDotEnv(path string) error {
	err := godotenv.Load(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
