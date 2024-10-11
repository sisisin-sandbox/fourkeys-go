package shared

import (
	"context"
	"log/slog"
	"os"
)

type contextKey string

const loggerKey contextKey = "logger"

func WithLogger(ctx context.Context) context.Context {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		//AddSource: true,
		Level: slog.LevelInfo,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.LevelKey:
				a = slog.Attr{
					Key:   "severity",
					Value: a.Value,
				}
			case slog.SourceKey:
				a = slog.Attr{
					Key:   "logging.googleapis.com/sourceLocation",
					Value: a.Value,
				}
			}
			return a
		},
	}))

	return context.WithValue(ctx, loggerKey, logger)
}

func SetLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

func LoggerFromContext(ctx context.Context) *slog.Logger {
	logger, ok := ctx.Value(loggerKey).(*slog.Logger)
	if !ok {
		panic("logger not found in context")
	}
	return logger
}
