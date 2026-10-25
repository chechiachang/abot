package instructions

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func Load(systemPath, soulPath string) (string, error) {
	system, err := os.ReadFile(systemPath)
	if err != nil {
		return "", fmt.Errorf("read system instructions: %w", err)
	}
	if strings.TrimSpace(string(system)) == "" {
		return "", errors.New("system instructions are empty")
	}
	result := strings.TrimSpace(string(system))
	if soulPath == "" {
		return result, nil
	}
	soul, err := os.ReadFile(soulPath)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return "", fmt.Errorf("read soul instructions: %w", err)
	}
	if strings.TrimSpace(string(soul)) != "" {
		result += "\n\n" + strings.TrimSpace(string(soul))
	}
	return result, nil
}
