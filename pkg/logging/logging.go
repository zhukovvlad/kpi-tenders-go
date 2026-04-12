package logging

import (
	"log/slog"
	"os"
)

// Setup returns a configured *slog.Logger.
// "local" → human-readable text; anything else → structured JSON.
func Setup(env string) *slog.Logger {
	var handler slog.Handler

	switch env {
	case "local":
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	default:
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	}

	return slog.New(handler)
}
