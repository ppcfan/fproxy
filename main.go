package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"fproxy/client"
	"fproxy/config"
	"fproxy/server"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	switch cfg.Mode {
	case config.ModeClient:
		c := client.New(cfg.Client, logger)
		if err := c.Start(); err != nil {
			logger.Error("failed to start client", "error", err)
			os.Exit(1)
		}
		logger.Info("client started", "listen", cfg.Client.ListenAddr, "servers", len(cfg.Client.Servers))

		<-sigCh
		logger.Info("shutting down...")
		c.Stop()

	case config.ModeServer:
		s := server.New(cfg.Server, logger)
		if err := s.Start(); err != nil {
			logger.Error("failed to start server", "error", err)
			os.Exit(1)
		}
		logger.Info("server started", "listen", len(cfg.Server.ListenAddrs), "target", cfg.Server.TargetAddr)

		<-sigCh
		logger.Info("shutting down...")
		s.Stop()
	}
}
