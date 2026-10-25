package policy

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const Version = 1

type Policy struct {
	Version             int                     `json:"version" mapstructure:"version"`
	Telegram            Telegram                `json:"telegram" mapstructure:"telegram"`
	Global              Capabilities            `json:"global" mapstructure:"global"`
	Defaults            Selection               `json:"defaults" mapstructure:"defaults"`
	ModelThinkingLevels map[string][]string     `json:"model_thinking_levels,omitempty" mapstructure:"model_thinking_levels"`
	ChatOverrides       map[string]ChatOverride `json:"chat_overrides,omitempty" mapstructure:"chat_overrides"`
	Grants              []Grant                 `json:"grants,omitempty" mapstructure:"grants"`
}

type Telegram struct {
	AllowedUserIDs     []int64 `json:"allowed_user_ids" mapstructure:"allowed_user_ids"`
	AdminUserIDs       []int64 `json:"admin_user_ids,omitempty" mapstructure:"admin_user_ids"`
	DefaultGroupID     int64   `json:"default_group_id" mapstructure:"default_group_id"`
	ApprovedChannelIDs []int64 `json:"approved_channel_ids,omitempty" mapstructure:"approved_channel_ids"`
}

type Capabilities struct {
	Tools          []string `json:"tools,omitempty" mapstructure:"tools"`
	Models         []string `json:"models,omitempty" mapstructure:"models"`
	Skills         []string `json:"skills,omitempty" mapstructure:"skills"`
	MCP            []string `json:"mcp,omitempty" mapstructure:"mcp"`
	ThinkingLevels []string `json:"thinking_levels,omitempty" mapstructure:"thinking_levels"`
}

type ConfiguredCapabilities struct {
	Global              Capabilities            `mapstructure:"global"`
	Defaults            Selection               `mapstructure:"defaults"`
	ModelThinkingLevels map[string][]string     `mapstructure:"model_thinking_levels"`
	ChatOverrides       map[string]ChatOverride `mapstructure:"chat_overrides"`
	Grants              []Grant                 `mapstructure:"grants"`
}

type Selection struct {
	Tools         []string `json:"tools,omitempty" mapstructure:"tools"`
	Model         string   `json:"model,omitempty" mapstructure:"model"`
	Skills        []string `json:"skills,omitempty" mapstructure:"skills"`
	MCP           []string `json:"mcp,omitempty" mapstructure:"mcp"`
	ThinkingLevel string   `json:"thinking_level,omitempty" mapstructure:"thinking_level"`
}

type ChatOverride struct {
	Tools         *[]string `json:"tools,omitempty" mapstructure:"tools"`
	Model         *string   `json:"model,omitempty" mapstructure:"model"`
	Skills        *[]string `json:"skills,omitempty" mapstructure:"skills"`
	MCP           *[]string `json:"mcp,omitempty" mapstructure:"mcp"`
	ThinkingLevel *string   `json:"thinking_level,omitempty" mapstructure:"thinking_level"`
}

type Grant struct {
	Kind      string `json:"kind" mapstructure:"kind"`
	ID        int64  `json:"id" mapstructure:"id"`
	GrantedBy string `json:"granted_by" mapstructure:"granted_by"`
	GrantedAt string `json:"granted_at" mapstructure:"granted_at"`
}

type Request struct {
	ChatType string
	ChatID   int64
	UserID   *int64
}

type Decision struct {
	Allowed bool
	Reason  string
}

func (p Policy) Validate() error {
	if p.Version != Version {
		return fmt.Errorf("policy version must be %d", Version)
	}
	if err := positiveUnique("telegram.allowed_user_ids", p.Telegram.AllowedUserIDs); err != nil {
		return err
	}
	if err := positiveUnique("telegram.admin_user_ids", p.Telegram.AdminUserIDs); err != nil {
		return err
	}
	if err := unique("telegram.approved_channel_ids", p.Telegram.ApprovedChannelIDs); err != nil {
		return err
	}
	for _, id := range p.Telegram.ApprovedChannelIDs {
		if id >= 0 {
			return errors.New("telegram.approved_channel_ids must contain negative channel IDs")
		}
	}
	if p.Telegram.DefaultGroupID > 0 {
		return errors.New("telegram.default_group_id must be a negative group ID")
	}
	for name, list := range map[string][]string{
		"global.tools": p.Global.Tools, "global.models": p.Global.Models,
		"global.skills": p.Global.Skills, "global.mcp": p.Global.MCP,
		"global.thinking_levels": p.Global.ThinkingLevels,
	} {
		if err := validNames(name, list); err != nil {
			return err
		}
	}
	for model, levels := range p.ModelThinkingLevels {
		if !slices.Contains(p.Global.Models, model) {
			return fmt.Errorf("model_thinking_levels contains unavailable model %q", model)
		}
		if err := validNames("model_thinking_levels."+model, levels); err != nil {
			return err
		}
		for _, level := range levels {
			if !slices.Contains(p.Global.ThinkingLevels, level) {
				return fmt.Errorf("thinking level %q for model %q is not in global.thinking_levels", level, model)
			}
		}
	}
	if p.Defaults.Model == "" {
		return errors.New("defaults.model must be selected explicitly")
	}
	if !slices.Contains(p.Global.Models, p.Defaults.Model) {
		return fmt.Errorf("default model %q is not in global.models", p.Defaults.Model)
	}
	if err := subset("defaults.tools", p.Defaults.Tools, p.Global.Tools); err != nil {
		return err
	}
	if err := subset("defaults.skills", p.Defaults.Skills, p.Global.Skills); err != nil {
		return err
	}
	if err := subset("defaults.mcp", p.Defaults.MCP, p.Global.MCP); err != nil {
		return err
	}
	if err := p.validateThinking(p.Defaults.Model, p.Defaults.ThinkingLevel); err != nil {
		return err
	}
	for chatID, override := range p.ChatOverrides {
		if id, err := strconv.ParseInt(chatID, 10, 64); err != nil || id == 0 {
			return fmt.Errorf("chat_overrides key %q must be a nonzero integer chat ID", chatID)
		}
		if override.Model != nil && !slices.Contains(p.Global.Models, *override.Model) {
			return fmt.Errorf("chat %s model %q is not in global.models", chatID, *override.Model)
		}
		if override.Tools != nil {
			if err := subset("chat_overrides."+chatID+".tools", *override.Tools, p.Global.Tools); err != nil {
				return err
			}
		}
		if override.Skills != nil {
			if err := subset("chat_overrides."+chatID+".skills", *override.Skills, p.Global.Skills); err != nil {
				return err
			}
		}
		if override.MCP != nil {
			if err := subset("chat_overrides."+chatID+".mcp", *override.MCP, p.Global.MCP); err != nil {
				return err
			}
		}
		model := p.Defaults.Model
		if override.Model != nil {
			model = *override.Model
		}
		if override.ThinkingLevel != nil {
			if err := p.validateThinking(model, *override.ThinkingLevel); err != nil {
				return fmt.Errorf("chat %s: %w", chatID, err)
			}
		}
	}
	for _, grant := range p.Grants {
		switch grant.Kind {
		case "private_user":
			if grant.ID <= 0 {
				return errors.New("private_user grant IDs must be positive user IDs")
			}
		case "group", "channel":
			if grant.ID >= 0 {
				return fmt.Errorf("%s grant IDs must be negative chat IDs", grant.Kind)
			}
		default:
			return fmt.Errorf("unsupported grant kind %q", grant.Kind)
		}
	}
	return nil
}

func (p Policy) Authorize(req Request) Decision {
	if req.UserID == nil || *req.UserID <= 0 {
		return Decision{Reason: "sender identity is missing"}
	}
	if !slices.Contains(p.Telegram.AllowedUserIDs, *req.UserID) {
		return Decision{Reason: "sender is not in telegram.allowed_user_ids"}
	}
	switch req.ChatType {
	case "private":
		if req.ChatID != *req.UserID {
			return Decision{Reason: "private chat ID does not match sender"}
		}
		return Decision{Allowed: true}
	case "group", "supergroup":
		if p.Telegram.DefaultGroupID == 0 || req.ChatID != p.Telegram.DefaultGroupID {
			return Decision{Reason: "group is not telegram.default_group_id"}
		}
		return Decision{Allowed: true}
	default:
		return Decision{Reason: "chat type cannot start agent turns"}
	}
}

func (p Policy) ChannelApproved(channelID int64) bool {
	if slices.Contains(p.Telegram.ApprovedChannelIDs, channelID) {
		return true
	}
	for _, grant := range p.Grants {
		if grant.Kind == "channel" && grant.ID == channelID {
			return true
		}
	}
	return false
}

func (p Policy) Effective(chatID int64) (Selection, error) {
	if err := p.Validate(); err != nil {
		return Selection{}, err
	}
	selected := p.Defaults
	if override, ok := p.ChatOverrides[strconv.FormatInt(chatID, 10)]; ok {
		if override.Tools != nil {
			selected.Tools = *override.Tools
		}
		if override.Model != nil {
			selected.Model = *override.Model
		}
		if override.Skills != nil {
			selected.Skills = *override.Skills
		}
		if override.MCP != nil {
			selected.MCP = *override.MCP
		}
		if override.ThinkingLevel != nil {
			selected.ThinkingLevel = *override.ThinkingLevel
		}
	}
	selected.Tools = intersect(selected.Tools, p.Global.Tools)
	selected.Skills = intersect(selected.Skills, p.Global.Skills)
	selected.MCP = intersect(selected.MCP, p.Global.MCP)
	if !slices.Contains(p.Global.Models, selected.Model) {
		return Selection{}, fmt.Errorf("chat model %q is not globally allowed", selected.Model)
	}
	if err := p.validateThinking(selected.Model, selected.ThinkingLevel); err != nil {
		return Selection{}, err
	}
	return selected, nil
}

func (p Policy) validateThinking(model, level string) error {
	if level == "" {
		return nil
	}
	if !slices.Contains(p.Global.ThinkingLevels, level) {
		return fmt.Errorf("thinking level %q is not in global.thinking_levels", level)
	}
	supported, ok := p.ModelThinkingLevels[model]
	if !ok || !slices.Contains(supported, level) {
		return fmt.Errorf("thinking level %q is not declared for model %q", level, model)
	}
	return nil
}

func positiveUnique(name string, values []int64) error {
	if err := unique(name, values); err != nil {
		return err
	}
	for _, value := range values {
		if value <= 0 {
			return fmt.Errorf("%s must contain positive IDs", name)
		}
	}
	return nil
}

func unique[T comparable](name string, values []T) error {
	seen := make(map[T]bool, len(values))
	for _, value := range values {
		if seen[value] {
			return fmt.Errorf("%s contains duplicate value %v", name, value)
		}
		seen[value] = true
	}
	return nil
}

func validNames(name string, values []string) error {
	if err := unique(name, values); err != nil {
		return err
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s cannot contain empty values", name)
		}
	}
	return nil
}

func subset(name string, values, allowed []string) error {
	if err := validNames(name, values); err != nil {
		return err
	}
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			return fmt.Errorf("%s value %q exceeds the global limit", name, value)
		}
	}
	return nil
}

func intersect(values, allowed []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if slices.Contains(allowed, value) {
			result = append(result, value)
		}
	}
	return result
}
