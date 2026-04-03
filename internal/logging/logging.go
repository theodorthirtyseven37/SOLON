package logging

import (
	"io"
	"log/slog"
	"os"
)

// Init sets up the global slog logger. When json is true, output is JSON;
// otherwise it uses the human-readable text handler.
func Init(json bool) {
	var handler slog.Handler
	if json {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})
	}
	slog.SetDefault(slog.New(handler))
}

// InitWith creates a logger writing to a specific writer (useful for testing).
func InitWith(w io.Writer, json bool) {
	var handler slog.Handler
	if json {
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})
	} else {
		handler = slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	slog.SetDefault(slog.New(handler))
}
