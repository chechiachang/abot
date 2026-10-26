package main

import (
	"context"
	"errors"
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
		logging.New().Error("could not load .env")
		os.Exit(1)
	}
	cmd := cli.NewRootCmd()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd.SetContext(ctx)
	if err := cmd.Execute(); err != nil {
		logger := logging.New(
			os.Getenv("ABOT_AZURE_API_KEY"), os.Getenv("AZURE_OPENAI_API_KEY"),
			os.Getenv("ABOT_TELEGRAM_BOT_TOKEN"), os.Getenv("TELEGRAM_BOT_TOKEN"),
			os.Getenv("ABOT_MORSEL_API_KEY"), os.Getenv("MORSEL_API_KEY"),
			os.Getenv("FIRECRAWL_API_KEY"), os.Getenv("LOGFIRE_TOKEN"),
		)
		logger.WithField("command", cmd.CommandPath()).WithError(err).Error("command failed")
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
