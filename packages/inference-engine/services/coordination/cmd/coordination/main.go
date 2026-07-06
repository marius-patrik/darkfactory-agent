package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/services/coordination/internal/election"
	"github.com/marius-patrik/agentos/inference-engine/services/coordination/internal/events"
	"github.com/marius-patrik/agentos/inference-engine/services/coordination/internal/ops"
	"github.com/marius-patrik/agentos/inference-engine/services/coordination/internal/queue"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	natsURL := env("NATS_URL", "nats://127.0.0.1:4222")
	nodeID := env("ROMMIE_NODE_ID", hostname())

	el, err := election.NewNATS(natsURL, nodeID, log)
	if err != nil {
		log.Error("election setup failed", "err", err)
		os.Exit(1)
	}
	defer el.Close()

	bus, err := events.NewNATS(natsURL, events.DefaultSubjectPrefix, log)
	if err != nil {
		log.Error("events setup failed", "err", err)
		os.Exit(1)
	}
	defer bus.Close()

	if dsn := os.Getenv("POSTGRES_DSN"); dsn != "" {
		pgq, err := queue.NewPG(ctx, dsn)
		if err != nil {
			log.Error("queue setup failed", "err", err)
			os.Exit(1)
		}
		defer pgq.Close()
		pgOps, err := ops.NewPGStore(ctx, dsn)
		if err != nil {
			log.Error("ops store setup failed", "err", err)
			os.Exit(1)
		}
		defer pgOps.Close()
		log.Info("postgres coordination stores ready")
	} else {
		log.Info("POSTGRES_DSN unset; queue and postgres ops store not started")
	}

	go func() {
		if err := el.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("election stopped", "err", err)
			stop()
		}
	}()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	last := el.IsLeader()
	log.Info("coordination started", "node_id", nodeID, "leader", last)
	for {
		select {
		case <-ctx.Done():
			_ = el.Release(context.Background())
			log.Info("coordination stopped")
			return
		case <-ticker.C:
			now := el.IsLeader()
			if now != last {
				log.Info("leadership transition", "leader", now)
				last = now
			}
		}
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "coordination-local"
	}
	return name
}

