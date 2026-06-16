package recipient

import "context"

// Source 定义收件人来源接口
type Source interface {
	Recipients(ctx context.Context) ([]string, error)
}
