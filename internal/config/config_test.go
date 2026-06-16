package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNormalizesAndDeduplicatesRecipients(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := `
source_mailbox:
  host: imap.example.com
  port: 993
  username: boss@example.com
  password: secret
  use_ssl: true
smtp:
  host: smtp.example.com
  port: 465
  username: boss@example.com
  password: secret
  from: boss@example.com
rules:
  - name: default
    enabled: true
    recipients:
      - User-A@Example.com
      - user-a@example.com
      - user-b@example.com
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	recipients := cfg.EnabledRecipients()
	if len(recipients) != 2 {
		t.Fatalf("recipients len = %d, want 2: %#v", len(recipients), recipients)
	}
	if recipients[0] != "user-a@example.com" || recipients[1] != "user-b@example.com" {
		t.Fatalf("recipients = %#v", recipients)
	}
	if cfg.State.File != filepath.Join(dir, "forwarder_state.json") {
		t.Fatalf("state file = %s", cfg.State.File)
	}
}

func TestLoadRejectsMissingRecipients(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := `
source_mailbox:
  host: imap.example.com
  username: boss@example.com
  password: secret
smtp:
  host: smtp.example.com
  username: boss@example.com
  password: secret
  from: boss@example.com
rules:
  - name: default
    enabled: false
    recipients:
      - user-a@example.com
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := cfg.ValidateRules(); err == nil {
		t.Fatal("ValidateRules() error = nil, want validation error")
	}
}

func TestLoadRulesFileSupportsRecipientsOnly(t *testing.T) {
	dir := t.TempDir()
	rulesPath := filepath.Join(dir, "rules.yaml")
	content := `
recipients:
  - User-A@Example.com
  - user-b@example.com
`
	if err := os.WriteFile(rulesPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	rules, err := LoadRulesFile(rulesPath)
	if err != nil {
		t.Fatalf("LoadRulesFile() error = %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("rules len = %d, want 1", len(rules))
	}
	if rules[0].Name != "dynamic" || !rules[0].Enabled {
		t.Fatalf("rule = %#v", rules[0])
	}
	if rules[0].Recipients[0] != "user-a@example.com" {
		t.Fatalf("recipient not normalized: %#v", rules[0].Recipients)
	}
}

func TestLoadRulesFileSupportsRules(t *testing.T) {
	dir := t.TempDir()
	rulesPath := filepath.Join(dir, "rules.yaml")
	content := `
rules:
  - name: team-a
    enabled: true
    recipients:
      - User-A@Example.com
`
	if err := os.WriteFile(rulesPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	rules, err := LoadRulesFile(rulesPath)
	if err != nil {
		t.Fatalf("LoadRulesFile() error = %v", err)
	}
	if len(rules) != 1 || rules[0].Name != "team-a" {
		t.Fatalf("rules = %#v", rules)
	}
	if rules[0].Recipients[0] != "user-a@example.com" {
		t.Fatalf("recipient not normalized: %#v", rules[0].Recipients)
	}
}
