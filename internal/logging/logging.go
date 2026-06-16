package logging

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"mail-forwarder/internal/config"
)

func New(cfg config.LogConfig) (*zap.Logger, func(), error) {
	level := zapcore.InfoLevel
	if cfg.Level != "" {
		if err := level.Set(cfg.Level); err != nil {
			level = zapcore.InfoLevel
		}
	}
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	console := zapcore.NewCore(zapcore.NewConsoleEncoder(encoderCfg), zapcore.AddSync(os.Stdout), level)
	cores := []zapcore.Core{console}
	closeFile := func() {}
	if cfg.File != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.File), 0o755); err != nil {
			return nil, closeFile, err
		}
		f, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, closeFile, err
		}
		closeFile = func() { _ = f.Close() }
		cores = append(cores, zapcore.NewCore(zapcore.NewJSONEncoder(encoderCfg), zapcore.AddSync(f), level))
	}
	logger := zap.New(zapcore.NewTee(cores...), zap.AddCaller())
	return logger, func() {
		_ = logger.Sync()
		closeFile()
	}, nil
}
