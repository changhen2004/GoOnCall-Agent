// GoOnCall Agent API 入口。
package main

import (
	"log/slog"
	"os"

	"gooncall-agent/internal/bootstrap"
)

func main() {
	app, err := bootstrap.New()
	if err != nil {
		slog.Error("bootstrap", "error", err)
		os.Exit(1)
	}
	if err := app.Run(); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
