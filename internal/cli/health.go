package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/chechiachang/abot/internal/config"
	"github.com/chechiachang/abot/internal/instructions"
	"github.com/chechiachang/abot/internal/media"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func healthCommand(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use: "healthcheck", Short: "Check local readiness without contacting providers",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(v)
			if err != nil {
				return err
			}
			if err := cfg.ValidateProvider(); err != nil {
				return err
			}
			if cfg.Telegram.BotToken == "" {
				return errors.New("Telegram bot token is required")
			}
			if _, _, err := configuredPolicy(cfg); err != nil {
				return err
			}
			if _, err := instructions.Load(cfg.Instructions.SystemFile, cfg.Instructions.SoulFile); err != nil {
				return err
			}
			if err := checkSQLitePath(cfg.Runtime.SQLitePath); err != nil {
				return err
			}
			_, err = media.NewStore(cfg.Media.Root, media.Limits{
				MaxBytes: cfg.Media.MaxBytes, MaxDuration: cfg.Media.MaxDuration, MaxPages: cfg.Media.MaxPages,
				MaxExtractBytes: cfg.Media.MaxExtractBytes, Pandoc: cfg.Media.Pandoc, PDFInfo: cfg.Media.PDFInfo,
				PDFToText: cfg.Media.PDFToText, FFmpeg: cfg.Media.FFmpeg, FFprobe: cfg.Media.FFprobe,
				Whisper: cfg.Media.Whisper, WhisperModel: cfg.Media.WhisperModel,
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "ready")
			return err
		},
	}
}

func checkSQLitePath(path string) error {
	if path == "" {
		return errors.New("runtime.sqlite_path is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("runtime state directory is unavailable")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("runtime state path is not a regular file")
		}
		file, err := os.Open(path)
		if err != nil {
			return errors.New("runtime state database is unreadable")
		}
		defer file.Close()
		header := make([]byte, 16)
		n, err := io.ReadFull(file, header)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return errors.New("runtime state database is unreadable")
		}
		if n != 0 && (n < 16 || string(header) != "SQLite format 3\x00") {
			return errors.New("runtime state database is invalid")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("runtime state path is unavailable")
	}
	probe, err := os.CreateTemp(dir, ".abot-health-*")
	if err != nil {
		return errors.New("runtime state directory is not writable")
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return errors.New("runtime state directory is not writable")
	}
	if err := os.Remove(name); err != nil {
		return errors.New("runtime state directory cleanup failed")
	}
	return nil
}
