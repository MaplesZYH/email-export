package config

import (
	"fmt"
	"net/mail"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	SourceMailbox MailboxConfig  `mapstructure:"source_mailbox"`
	SMTP          SMTPConfig     `mapstructure:"smtp"`
	Rules         []RuleConfig   `mapstructure:"rules"`
	Forward       ForwardConfig  `mapstructure:"forward"`
	Database      DatabaseConfig `mapstructure:"database"`
	Daemon        DaemonConfig   `mapstructure:"daemon"`
	State         StateConfig    `mapstructure:"state"`
	Log           LogConfig      `mapstructure:"log"`
}

type DatabaseConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Name     string `mapstructure:"name"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	SSLMode  string `mapstructure:"ssl_mode"`
}

type DaemonConfig struct {
	Enabled         bool          `mapstructure:"enabled"`
	SyncInterval    time.Duration `mapstructure:"sync_interval"`
	RecipientsSource string       `mapstructure:"recipients_source"` // "database" | "static"
}

type MailboxConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	UseSSL   bool   `mapstructure:"use_ssl"`
	Folder   string `mapstructure:"folder"`
}

type SMTPConfig struct {
	Host          string `mapstructure:"host"`
	Port          int    `mapstructure:"port"`
	Username      string `mapstructure:"username"`
	Password      string `mapstructure:"password"`
	UseSSL        bool   `mapstructure:"use_ssl"`
	StartTLS      bool   `mapstructure:"start_tls"`
	From          string `mapstructure:"from"`
	FromName      string `mapstructure:"from_name"`
	SubjectPrefix string `mapstructure:"subject_prefix"`
}

type RuleConfig struct {
	Name       string   `mapstructure:"name"`
	Enabled    bool     `mapstructure:"enabled"`
	Recipients []string `mapstructure:"recipients"`
}

type ForwardConfig struct {
	DryRun                      bool     `mapstructure:"dry_run"`
	RequireAttachments          bool     `mapstructure:"require_attachments"`
	MaxMessagesPerRun           int      `mapstructure:"max_messages_per_run"`
	AllowedAttachmentExtensions []string `mapstructure:"allowed_attachment_extensions"`
}

type StateConfig struct {
	File string `mapstructure:"file"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
	File  string `mapstructure:"file"`
}

func Load(path string) (Config, error) {
	v := viper.New()
	setDefaults(v)
	v.SetConfigFile(path)
	v.SetEnvPrefix("MAIL_FORWARDER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := v.ReadInConfig(); err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, err
	}
	cfg.normalize(path)
	if err := cfg.ValidateBase(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func LoadRulesFile(path string) ([]RuleConfig, error) {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}
	var wrapper struct {
		Rules      []RuleConfig `mapstructure:"rules"`
		Recipients []string     `mapstructure:"recipients"`
	}
	if err := v.Unmarshal(&wrapper); err != nil {
		return nil, err
	}
	if len(wrapper.Rules) > 0 {
		cfg := Config{Rules: wrapper.Rules}
		cfg.NormalizeRules()
		return cfg.Rules, nil
	}
	if len(wrapper.Recipients) > 0 {
		cfg := Config{Rules: []RuleConfig{{
			Name:       "dynamic",
			Enabled:    true,
			Recipients: wrapper.Recipients,
		}}}
		cfg.NormalizeRules()
		return cfg.Rules, nil
	}
	return nil, fmt.Errorf("动态规则文件中没有 rules 或 recipients")
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("source_mailbox.port", 993)
	v.SetDefault("source_mailbox.use_ssl", true)
	v.SetDefault("source_mailbox.folder", "INBOX")
	v.SetDefault("smtp.port", 465)
	v.SetDefault("smtp.use_ssl", true)
	v.SetDefault("smtp.subject_prefix", "[简历转发]")
	v.SetDefault("forward.dry_run", true)
	v.SetDefault("forward.require_attachments", true)
	v.SetDefault("forward.max_messages_per_run", 50)
	v.SetDefault("forward.allowed_attachment_extensions", []string{".pdf", ".doc", ".docx"})
	v.SetDefault("state.file", "forwarder_state.json")
	v.SetDefault("log.level", "info")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.ssl_mode", "disable")
	v.SetDefault("daemon.enabled", false)
	v.SetDefault("daemon.sync_interval", "5m")
	v.SetDefault("daemon.recipients_source", "static")
}

func (c *Config) normalize(configPath string) {
	c.SourceMailbox.Host = strings.TrimSpace(c.SourceMailbox.Host)
	c.SourceMailbox.Username = strings.TrimSpace(c.SourceMailbox.Username)
	c.SourceMailbox.Folder = strings.TrimSpace(c.SourceMailbox.Folder)
	if c.SourceMailbox.Folder == "" {
		c.SourceMailbox.Folder = "INBOX"
	}
	c.SMTP.Host = strings.TrimSpace(c.SMTP.Host)
	c.SMTP.Username = strings.TrimSpace(c.SMTP.Username)
	c.SMTP.From = strings.TrimSpace(c.SMTP.From)
	c.SMTP.FromName = strings.TrimSpace(c.SMTP.FromName)
	c.SMTP.SubjectPrefix = strings.TrimSpace(c.SMTP.SubjectPrefix)
	if c.SMTP.From == "" {
		c.SMTP.From = c.SMTP.Username
	}
	c.NormalizeRules()
	for i := range c.Forward.AllowedAttachmentExtensions {
		ext := strings.ToLower(strings.TrimSpace(c.Forward.AllowedAttachmentExtensions[i]))
		if ext != "" && !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		c.Forward.AllowedAttachmentExtensions[i] = ext
	}
	if c.State.File != "" && !filepath.IsAbs(c.State.File) {
		c.State.File = filepath.Join(filepath.Dir(configPath), c.State.File)
	}
	if c.Log.File != "" && !filepath.IsAbs(c.Log.File) {
		c.Log.File = filepath.Join(filepath.Dir(configPath), c.Log.File)
	}
}

func (c *Config) NormalizeRules() {
	for i := range c.Rules {
		c.Rules[i].Name = strings.TrimSpace(c.Rules[i].Name)
		for j := range c.Rules[i].Recipients {
			c.Rules[i].Recipients[j] = strings.ToLower(strings.TrimSpace(c.Rules[i].Recipients[j]))
		}
	}
}

func (c Config) ValidateBase() error {
	if c.SourceMailbox.Host == "" || c.SourceMailbox.Port <= 0 || c.SourceMailbox.Username == "" || c.SourceMailbox.Password == "" {
		return fmt.Errorf("source_mailbox 配置不完整")
	}
	if c.SMTP.Host == "" || c.SMTP.Port <= 0 || c.SMTP.Username == "" || c.SMTP.Password == "" || c.SMTP.From == "" {
		return fmt.Errorf("smtp 配置不完整")
	}
	if _, err := mail.ParseAddress(c.SMTP.From); err != nil {
		return fmt.Errorf("smtp.from 不是合法邮箱: %w", err)
	}
	if c.Forward.MaxMessagesPerRun <= 0 {
		return fmt.Errorf("forward.max_messages_per_run 必须大于 0")
	}
	if c.State.File == "" {
		return fmt.Errorf("state.file 不能为空")
	}
	if c.Daemon.RecipientsSource == "database" {
		if c.Database.Host == "" || c.Database.Name == "" || c.Database.User == "" {
			return fmt.Errorf("database 配置不完整 (需要 host, name, user)")
		}
	}
	if c.Daemon.RecipientsSource != "database" && c.Daemon.RecipientsSource != "static" {
		return fmt.Errorf("daemon.recipients_source 必须是 database 或 static")
	}
	if c.Daemon.SyncInterval < time.Minute {
		return fmt.Errorf("daemon.sync_interval 不能小于 1 分钟")
	}
	return nil
}

func (c Config) ValidateRules() error {
	if c.Daemon.RecipientsSource == "database" {
		return nil // 收件人从数据库动态获取，不需要静态规则
	}
	if len(c.Rules) == 0 {
		return fmt.Errorf("至少需要配置一个分发规则")
	}
	if len(c.EnabledRecipients()) == 0 {
		return fmt.Errorf("启用的分发规则没有有效收件人")
	}
	for _, recipient := range c.EnabledRecipients() {
		if _, err := mail.ParseAddress(recipient); err != nil {
			return fmt.Errorf("收件人邮箱不合法 %s: %w", recipient, err)
		}
	}
	return nil
}

func (c Config) EnabledRecipients() []string {
	seen := make(map[string]struct{})
	var recipients []string
	for _, rule := range c.Rules {
		if !rule.Enabled {
			continue
		}
		for _, recipient := range rule.Recipients {
			recipient = strings.ToLower(strings.TrimSpace(recipient))
			if recipient == "" {
				continue
			}
			if _, ok := seen[recipient]; ok {
				continue
			}
			seen[recipient] = struct{}{}
			recipients = append(recipients, recipient)
		}
	}
	return recipients
}
