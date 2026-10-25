package cli

import (
	"errors"

	"github.com/chechiachang/abot/internal/config"
	"github.com/chechiachang/abot/internal/engine"
	"github.com/chechiachang/abot/internal/logging"
	"github.com/chechiachang/abot/internal/media"
	"github.com/chechiachang/abot/internal/policy"
	agentruntime "github.com/chechiachang/abot/internal/runtime"
	"github.com/chechiachang/abot/internal/telegram"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func telegramCommand(v *viper.Viper) *cobra.Command {
	command := &cobra.Command{Use: "telegram", Short: "Run the Telegram bridge"}
	command.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Poll Telegram and route authorized turns to ADK-Go",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			if cfg.Telegram.BotToken == "" {
				return errors.New("Telegram bot token is required; set TELEGRAM_BOT_TOKEN")
			}
			mediaStore, err := media.NewStore(cfg.Media.Root, media.Limits{
				MaxBytes: cfg.Media.MaxBytes, MaxDuration: cfg.Media.MaxDuration, MaxPages: cfg.Media.MaxPages,
				MaxExtractBytes: cfg.Media.MaxExtractBytes, Pandoc: cfg.Media.Pandoc, PDFInfo: cfg.Media.PDFInfo,
				PDFToText: cfg.Media.PDFToText, FFmpeg: cfg.Media.FFmpeg, FFprobe: cfg.Media.FFprobe,
				Whisper: cfg.Media.Whisper, WhisperModel: cfg.Media.WhisperModel,
			})
			if err != nil {
				return err
			}
			if err := cfg.ValidateProvider(); err != nil {
				return err
			}
			current, _, err := configuredPolicy(cfg)
			if err != nil {
				return err
			}
			if _, err := current.Effective(0); err != nil {
				return err
			}
			logger := logging.New(cfg.Azure.APIKey, cfg.Telegram.BotToken, cfg.Telegram.MorselAPIKey, cfg.URLReader.FirecrawlAPIKey, cfg.Logfire.Token)
			level, _ := logrus.ParseLevel(cfg.Log.Level)
			logger.SetLevel(level)
			stopTracing := configureTracing(cmd.Context(), cfg, logger)
			defer stopTracing()
			client, err := telegram.NewClient(cfg.Telegram.BotToken)
			if err != nil {
				return err
			}
			var publisher telegram.Publisher
			if cfg.Telegram.MorselAPIKey != "" {
				publisher, err = telegram.NewConfiguredMorsel(cfg.Telegram.MorselPublishURL, cfg.Telegram.MorselAPIKey)
				if err != nil {
					return err
				}
			}
			state, err := agentruntime.Open(cfg.Runtime.SQLitePath)
			if err != nil {
				return err
			}
			defer state.Close()
			host := &engine.Engine{
				Config: cfg, State: state, Turns: agentruntime.NewTurnManager(), Media: mediaStore, Logger: logger,
				LoadPolicy: func() (policy.Policy, error) {
					active, _, err := configuredPolicy(cfg)
					return active, err
				},
			}
			bridge := telegram.Bridge{Config: cfg, State: state, Engine: host, Media: mediaStore, Client: client, Publisher: publisher, Logger: logger}
			return bridge.Run(cmd.Context())
		},
	})
	return command
}
