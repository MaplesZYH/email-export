package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"mail-forwarder/internal/config"
	"mail-forwarder/internal/forwarder"
	"mail-forwarder/internal/logging"
	"mail-forwarder/internal/mailbox"
	"mail-forwarder/internal/recipient"
	"mail-forwarder/internal/state"
)

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	dryRun := flag.Bool("dry-run", false, "只打印转发计划，不真实发送")
	rulesFile := flag.String("rules-file", "", "动态分发规则文件路径")
	var recipientsFlags recipientFlags
	flag.Var(&recipientsFlags, "recipient", "动态目标邮箱，可重复传入")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}
	if *rulesFile != "" {
		rules, err := config.LoadRulesFile(*rulesFile)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "加载动态分发规则失败: %v\n", err)
			os.Exit(1)
		}
		cfg.Rules = rules
	}
	if len(recipientsFlags) > 0 {
		cfg.Rules = []config.RuleConfig{{
			Name:       "dynamic",
			Enabled:    true,
			Recipients: recipientsFlags,
		}}
	}
	cfg.NormalizeRules()
	if flag.Lookup("dry-run") != nil && isFlagSet("dry-run") {
		cfg.Forward.DryRun = *dryRun
	}
	if err := cfg.ValidateRules(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "分发规则无效: %v\n", err)
		os.Exit(1)
	}

	logger, cleanup, err := logging.New(cfg.Log)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// 创建收件人来源
	src, srcCleanup, err := newRecipientSource(ctx, cfg)
	if err != nil {
		logger.Error("初始化收件人来源失败", zap.Error(err))
		os.Exit(1)
	}
	defer srcCleanup()

	if cfg.Daemon.Enabled {
		runDaemon(ctx, cfg, logger, src)
	} else {
		if err := runOnce(ctx, cfg, logger, src); err != nil {
			logger.Error("邮件分发执行失败", zap.Error(err))
			os.Exit(1)
		}
	}
}

func newRecipientSource(ctx context.Context, cfg config.Config) (recipient.Source, func(), error) {
	switch cfg.Daemon.RecipientsSource {
	case "database":
		dbSrc, err := recipient.NewDatabaseSource(ctx, cfg.Database)
		if err != nil {
			return nil, nil, fmt.Errorf("连接数据库失败: %w", err)
		}
		return dbSrc, dbSrc.Close, nil
	default: // "static"
		return recipient.NewStaticSource(cfg), func() {}, nil
	}
}

func runDaemon(ctx context.Context, cfg config.Config, logger *zap.Logger, src recipient.Source) {
	interval := cfg.Daemon.SyncInterval
	logger.Info("启动 daemon 模式", zap.Duration("sync_interval", interval), zap.String("recipients_source", cfg.Daemon.RecipientsSource))

	// 启动时立即执行一次
	if err := runOnce(ctx, cfg, logger, src); err != nil {
		logger.Warn("首次同步失败", zap.Error(err))
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("收到退出信号，daemon 停止")
			return
		case <-ticker.C:
			syncCtx, syncCancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if err := runOnce(syncCtx, cfg, logger, src); err != nil {
				logger.Warn("定时同步失败", zap.Error(err))
			}
			syncCancel()
		}
	}
}

func runOnce(ctx context.Context, cfg config.Config, logger *zap.Logger, src recipient.Source) error {
	recipients, err := src.Recipients(ctx)
	if err != nil {
		return fmt.Errorf("获取收件人列表失败: %w", err)
	}
	if len(recipients) == 0 {
		logger.Info("没有启用的收件人，跳过本次同步")
		return nil
	}
	logger.Info("获取到收件人列表", zap.Int("count", len(recipients)), zap.Strings("recipients", recipients))

	store, err := state.Load(cfg.State.File)
	if err != nil {
		return err
	}

	client := mailbox.NewClient(cfg.SourceMailbox, cfg.Forward)
	fetchResult, err := client.FetchNew(ctx, store.LastUID)
	if err != nil {
		return err
	}
	if fetchResult.Initialized {
		logger.Info("首次运行已初始化邮箱游标，本次不转发历史邮件", zap.Uint32("last_uid", fetchResult.NextUID))
		store.LastUID = fetchResult.NextUID
		if cfg.Forward.DryRun {
			logger.Info("dry-run 模式不写入状态文件")
			return nil
		}
		return store.Save(cfg.State.File)
	}
	if len(fetchResult.Messages) == 0 {
		logger.Info("没有需要转发的新邮件", zap.Uint32("last_uid", store.LastUID))
		return nil
	}

	service := forwarder.New(cfg, recipients, logger)
	result, err := service.Forward(ctx, fetchResult.Messages, store)
	if err != nil {
		return err
	}
	logger.Info(
		"邮件分发执行完成",
		zap.Int("messages", len(fetchResult.Messages)),
		zap.Int("planned", result.Planned),
		zap.Int("sent", result.Sent),
		zap.Int("skipped", result.Skipped),
		zap.Int("failed", result.Failed),
		zap.Bool("dry_run", cfg.Forward.DryRun),
	)

	if fetchResult.NextUID > store.LastUID {
		store.LastUID = fetchResult.NextUID
	}
	if cfg.Forward.DryRun {
		logger.Info("dry-run 模式不写入状态文件")
		return nil
	}
	return store.Save(cfg.State.File)
}

func isFlagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

type recipientFlags []string

func (r *recipientFlags) String() string {
	return fmt.Sprint([]string(*r))
}

func (r *recipientFlags) Set(value string) error {
	*r = append(*r, value)
	return nil
}
