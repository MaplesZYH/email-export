package recipient

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"mail-forwarder/internal/config"
)

// DatabaseSource 从 talent-bank 的 resume_mailbox_settings 表查询 enabled 的 email_address
type DatabaseSource struct {
	pool *pgxpool.Pool
}

func NewDatabaseSource(ctx context.Context, cfg config.DatabaseConfig) (*DatabaseSource, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Name, cfg.SSLMode)
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析数据库连接字符串失败: %w", err)
	}
	config.MinConns = 1
	config.MaxConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("数据库连接测试失败: %w", err)
	}
	return &DatabaseSource{pool: pool}, nil
}

func (s *DatabaseSource) Recipients(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT email_address FROM resume_mailbox_settings WHERE status = 'enabled' AND deleted_at IS NULL")
	if err != nil {
		return nil, fmt.Errorf("查询收件人失败: %w", err)
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	var addresses []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, fmt.Errorf("读取收件人失败: %w", err)
		}
		addr = strings.ToLower(strings.TrimSpace(addr))
		if addr == "" {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		addresses = append(addresses, addr)
	}
	return addresses, rows.Err()
}

func (s *DatabaseSource) Close() {
	s.pool.Close()
}
