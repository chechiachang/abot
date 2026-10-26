package cli

import (
	"context"
	"time"

	"github.com/chechiachang/abot/internal/config"
	"github.com/chechiachang/abot/internal/observability"
	"github.com/sirupsen/logrus"
)

func configureTracing(ctx context.Context, cfg config.Config, logger *logrus.Logger) func() {
	shutdown, err := observability.Start(ctx, cfg.Logfire.Endpoint, cfg.Logfire.Token, cfg.Logfire.Environment)
	if err != nil {
		logger.WithError(err).Warn("Logfire tracing disabled")
		return func() {}
	}
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(shutdownCtx); err != nil {
			logger.WithError(err).Warn("Logfire tracing shutdown failed")
		}
	}
}
