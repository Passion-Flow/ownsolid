package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring/alerting"
)

func main() {
	file := flag.String("config", "", "protected relay JSON configuration file")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if *file == "" || flag.NArg() != 0 {
		logger.Error("specify a relay configuration file with -config")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := alerting.RunFeishuRelay(ctx, *file, logger); err != nil {
		logger.Error("alert relay stopped", "error", err)
		os.Exit(1)
	}
}
