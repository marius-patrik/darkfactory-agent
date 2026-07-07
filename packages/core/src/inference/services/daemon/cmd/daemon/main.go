package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/nats-io/nats.go"

	"github.com/marius-patrik/agentos/inference-engine/services/daemon/internal/natslane"
	"github.com/marius-patrik/agentos/inference-engine/services/daemon/internal/ops"
	"github.com/marius-patrik/agentos/inference-engine/services/daemon/internal/toolexec"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	natsURL := env("NATS_URL", "nats://127.0.0.1:4222")
	hostID := env("ROMMIE_HOST_ID", hostname())
	root := env("ROMMIE_WORKDIR_ROOT", ".")

	exec, err := toolexec.New(root)
	if err != nil {
		log.Error("tool executor setup failed", "err", err)
		os.Exit(1)
	}
	nc, err := nats.Connect(natsURL)
	if err != nil {
		log.Error("nats connect failed", "err", err)
		os.Exit(1)
	}
	defer nc.Close()

	lane := natslane.New(exec, ops.NewBroker(ops.NewMemStore()), log)
	subject := "rommie.exec.tool." + hostID
	sub, err := lane.Subscribe(ctx, nc, subject)
	if err != nil {
		log.Error("nats subscribe failed", "err", err)
		os.Exit(1)
	}
	defer sub.Unsubscribe()

	log.Info("daemon started", "host_id", hostID, "subject", subject, "workdir_root", root)
	<-ctx.Done()
	log.Info("daemon stopped")
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
		return "daemon-local"
	}
	return name
}

