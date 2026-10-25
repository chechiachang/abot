package policy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxPolicyBytes = 1 << 20

type Store struct {
	PolicyPath string
	AuditPath  string
}

type AuditEvent struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Before string    `json:"before"`
	After  string    `json:"after"`
}

func DefaultStore() (Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return Store{}, fmt.Errorf("find user config directory: %w", err)
	}
	dir = filepath.Join(dir, "abot")
	return Store{PolicyPath: filepath.Join(dir, "policy.json"), AuditPath: filepath.Join(dir, "audit.jsonl")}, nil
}

func LoadFile(path string) (Policy, error) {
	data, err := readLimited(path)
	if err != nil {
		return Policy{}, err
	}
	var candidate Policy
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&candidate); err != nil {
		return Policy{}, fmt.Errorf("decode policy file: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Policy{}, errors.New("policy file must contain exactly one JSON object")
	}
	if err := candidate.Validate(); err != nil {
		return Policy{}, err
	}
	return candidate, nil
}

func (s Store) Load() (Policy, bool, error) {
	if s.PolicyPath == "" {
		return Policy{}, false, errors.New("policy file path is required")
	}
	if _, err := os.Stat(s.PolicyPath); errors.Is(err, os.ErrNotExist) {
		return Policy{}, false, nil
	} else if err != nil {
		return Policy{}, false, fmt.Errorf("inspect policy file: %w", err)
	}
	policy, err := LoadFile(s.PolicyPath)
	return policy, true, err
}

func (s Store) Apply(candidate Policy, actor string) error {
	if err := candidate.Validate(); err != nil {
		return err
	}
	if s.PolicyPath == "" {
		return errors.New("policy file path is required")
	}
	before := "none"
	if current, exists, err := s.Load(); err != nil {
		return err
	} else if exists {
		before = current.Summary()
	}
	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return fmt.Errorf("encode policy: %w", err)
	}
	if err := atomicWrite(s.PolicyPath, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write policy: %w", err)
	}
	if s.AuditPath != "" {
		event := AuditEvent{At: time.Now().UTC(), Actor: safeActor(actor), Action: "policy.apply", Before: before, After: candidate.Summary()}
		if err := appendAudit(s.AuditPath, event); err != nil {
			return fmt.Errorf("policy applied but audit write failed: %w", err)
		}
	}
	return nil
}

func (p Policy) Summary() string {
	return fmt.Sprintf("users=%d group=%d channels=%d model=%q tools=%d skills=%d mcp=%d chats=%d grants=%d",
		len(p.Telegram.AllowedUserIDs), p.Telegram.DefaultGroupID, len(p.Telegram.ApprovedChannelIDs), p.Defaults.Model,
		len(p.Defaults.Tools), len(p.Defaults.Skills), len(p.Defaults.MCP), len(p.ChatOverrides), len(p.Grants))
}

func readLimited(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open policy file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxPolicyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read policy file: %w", err)
	}
	if len(data) > maxPolicyBytes {
		return nil, errors.New("policy file exceeds 1 MiB limit")
	}
	return data, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".policy-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func appendAudit(path string, event AuditEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	writer := bufio.NewWriter(file)
	if _, err := writer.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := writer.Flush(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func safeActor(actor string) string {
	var out strings.Builder
	for _, c := range actor {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' {
			out.WriteRune(c)
		}
		if out.Len() >= 64 {
			break
		}
	}
	if out.Len() == 0 {
		return "local"
	}
	return out.String()
}
