package recipient

import (
	"context"

	"mail-forwarder/internal/config"
)

// StaticSource 从配置文件的 rules 中获取收件人（原有逻辑，作为 fallback）
type StaticSource struct {
	recipients []string
}

func NewStaticSource(cfg config.Config) *StaticSource {
	return &StaticSource{recipients: cfg.EnabledRecipients()}
}

func (s *StaticSource) Recipients(_ context.Context) ([]string, error) {
	return s.recipients, nil
}
