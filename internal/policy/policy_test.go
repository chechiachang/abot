package policy

import (
	"strings"
	"testing"
)

func TestAuthorizeRequiresListedSenderAndSingleConfiguredGroup(t *testing.T) {
	p := testPolicy()
	p.Telegram.AdminUserIDs = []int64{10, 12}
	listed, unlisted := int64(10), int64(11)
	for _, test := range []struct {
		name string
		req  Request
		want bool
	}{
		{"listed private", Request{ChatType: "private", ChatID: 10, UserID: &listed}, true},
		{"unlisted private", Request{ChatType: "private", ChatID: 11, UserID: &unlisted}, false},
		{"listed in default group", Request{ChatType: "group", ChatID: -100, UserID: &listed}, true},
		{"listed in other group", Request{ChatType: "group", ChatID: -200, UserID: &listed}, false},
		{"unlisted in default group", Request{ChatType: "group", ChatID: -100, UserID: &unlisted}, false},
		{"anonymous", Request{ChatType: "private", ChatID: 10}, false},
		{"admin is not allowlisted", Request{ChatType: "private", ChatID: 12, UserID: ptr(int64(12))}, false},
		{"channel cannot start turn", Request{ChatType: "channel", ChatID: -300, UserID: &listed}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := p.Authorize(test.req)
			if got.Allowed != test.want {
				t.Fatalf("decision = %#v, want allowed=%v", got, test.want)
			}
		})
	}
}

func TestEmptyAllowlistDeniesEvenConfiguredAdministrator(t *testing.T) {
	p := testPolicy()
	p.Telegram.AllowedUserIDs = nil
	p.Telegram.AdminUserIDs = []int64{10}
	decision := p.Authorize(Request{ChatType: "private", ChatID: 10, UserID: ptr(int64(10))})
	if decision.Allowed || !strings.Contains(decision.Reason, "allowed_user_ids") {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestEffectivePolicyAppliesChatOverrideWithinGlobalLimit(t *testing.T) {
	p := testPolicy()
	tools := []string{"read", "edit"}
	model := "model-b"
	p.Global.Models = []string{"model-a", "model-b"}
	p.Defaults.Tools = []string{"read", "write"}
	p.ChatOverrides = map[string]ChatOverride{"20": {Tools: &tools, Model: &model}}

	selected, err := p.Effective(20)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Model != model || strings.Join(selected.Tools, ",") != "read,edit" {
		t.Fatalf("effective selection = %#v", selected)
	}
}

func TestValidateRejectsUnavailableModelAndThinkingLevel(t *testing.T) {
	p := testPolicy()
	p.Defaults.Model = "unknown"
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "global.models") {
		t.Fatalf("unavailable model error = %v", err)
	}
	p = testPolicy()
	p.Global.ThinkingLevels = []string{"none"}
	p.Defaults.ThinkingLevel = "high"
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "global.thinking_levels") {
		t.Fatalf("unsupported thinking level error = %v", err)
	}
}

func TestChannelGrantsOnlyAuthorizePassiveChannelIngestion(t *testing.T) {
	p := testPolicy()
	p.Grants = []Grant{{Kind: "channel", ID: -300}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if !p.ChannelApproved(-300) || p.Authorize(Request{ChatType: "channel", ChatID: -300, UserID: ptr(int64(10))}).Allowed {
		t.Fatal("channel grant must approve passive ingestion without allowing an agent turn")
	}
}

func TestPrivateAndGroupGrantsCannotBypassAllowlistOrDefaultGroup(t *testing.T) {
	p := testPolicy()
	p.Grants = []Grant{{Kind: "private_user", ID: 11}, {Kind: "group", ID: -200}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	unlisted := int64(11)
	if p.Authorize(Request{ChatType: "private", ChatID: 11, UserID: &unlisted}).Allowed {
		t.Fatal("private-user grant bypassed the base user allowlist")
	}
	listed := int64(10)
	if p.Authorize(Request{ChatType: "group", ChatID: -200, UserID: &listed}).Allowed {
		t.Fatal("group grant bypassed the configured default group")
	}
}

func testPolicy() Policy {
	return Policy{
		Version:  Version,
		Telegram: Telegram{AllowedUserIDs: []int64{10}, DefaultGroupID: -100},
		Global:   Capabilities{Tools: []string{"read", "edit", "write"}, Models: []string{"model-a"}, Skills: []string{"base"}, MCP: []string{"docs"}, ThinkingLevels: []string{"none", "low"}},
		Defaults: Selection{Tools: []string{"read", "write"}, Model: "model-a", Skills: []string{"base"}, MCP: []string{"docs"}},
	}
}

func ptr[T any](value T) *T { return &value }
