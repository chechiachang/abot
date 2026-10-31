package cli

import (
	"errors"
	"fmt"

	"github.com/chechiachang/abot/internal/config"
	"github.com/chechiachang/abot/internal/logging"
	"github.com/chechiachang/abot/internal/media"
	"github.com/chechiachang/abot/internal/urls"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func urlCommand(v *viper.Viper) *cobra.Command {
	command := &cobra.Command{Use: "url", Short: "Read bounded content from public URLs"}
	command.AddCommand(&cobra.Command{
		Use:   "read <url>",
		Short: "Extract text from a public HTTP(S) URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			logger := logging.New(cfg.Azure.APIKey, cfg.Telegram.BotToken, cfg.Telegram.MorselAPIKey, cfg.URLReader.FirecrawlAPIKey, cfg.Logfire.Token, cfg.R2.AccessKeyID, cfg.R2.SecretAccessKey)
			level, _ := logrus.ParseLevel(cfg.Log.Level)
			logger.SetLevel(level)
			stopTracing := configureTracing(cmd.Context(), cfg, logger)
			defer stopTracing()
			store, err := media.NewStore(cfg.Media.Root, media.Limits{
				MaxBytes: cfg.Media.MaxBytes, MaxDuration: cfg.Media.MaxDuration, MaxPages: cfg.Media.MaxPages,
				MaxExtractBytes: cfg.Media.MaxExtractBytes, Pandoc: cfg.Media.Pandoc, PDFInfo: cfg.Media.PDFInfo,
				PDFToText: cfg.Media.PDFToText, FFmpeg: cfg.Media.FFmpeg, FFprobe: cfg.Media.FFprobe,
				Whisper: cfg.Media.Whisper, WhisperModel: cfg.Media.WhisperModel,
			})
			if err != nil {
				return err
			}
			reader, err := urls.NewReader(urls.Config{
				Timeout: cfg.URLReader.Timeout, MaxBytes: cfg.URLReader.MaxBytes, MaxOutputBytes: cfg.URLReader.MaxOutputBytes,
				MaxRedirects: cfg.URLReader.MaxRedirects, MaxAudioDuration: cfg.Media.MaxDuration, FirecrawlEndpoint: cfg.URLReader.FirecrawlEndpoint,
				FirecrawlAPIKey: cfg.URLReader.FirecrawlAPIKey, FirecrawlExternalTransfer: cfg.URLReader.FirecrawlExternalTransfer,
				BrowserExecutable: cfg.URLReader.BrowserExecutable, YouTubeDL: cfg.Media.YouTubeDL,
			}, store)
			if err != nil {
				return err
			}
			result, err := reader.Read(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if result.Title != "" {
				cmd.Println(result.Title)
			}
			cmd.Printf("Source: %s\nURL: %s\n\n%s\n", result.Source, result.FinalURL, result.Content)
			return nil
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Validate URL-reader settings without connecting",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			if cfg.URLReader.FirecrawlExternalTransfer && cfg.URLReader.FirecrawlAPIKey == "" {
				return errors.New("FIRECRAWL_API_KEY is required for enabled Firecrawl fallback")
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "URL reader configuration valid; Firecrawl external transfer enabled=%t\n", cfg.URLReader.FirecrawlExternalTransfer)
			return err
		},
	})
	return command
}
