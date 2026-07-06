package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/dispatchnet"
)

type config struct {
	natsURL   string
	namespace string
	agentID   string
	allowNoop bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: comms-helper <publish|subscribe|heartbeat> [flags]")
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	switch args[0] {
	case "publish":
		return runPublish(args[1:], log)
	case "subscribe":
		return runSubscribe(args[1:], log)
	case "heartbeat":
		return runHeartbeat(args[1:], log)
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runPublish(args []string, log *slog.Logger) error {
	fs, cfg := newFlagSet("publish")
	topic := fs.String("topic", "", "topic to publish to")
	payloadText := fs.String("payload", "", "JSON payload string")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !validTopic(*topic) {
		return fmt.Errorf("invalid --topic %q", *topic)
	}

	d, err := newDispatcher(cfg, log)
	if err != nil {
		return err
	}
	defer d.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return d.Publish(ctx, *topic, parsePayload(*payloadText))
}

func runSubscribe(args []string, log *slog.Logger) error {
	fs, cfg := newFlagSet("subscribe")
	topic := fs.String("topic", "", "topic to subscribe to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !validTopic(*topic) {
		return fmt.Errorf("invalid --topic %q", *topic)
	}

	d, err := newDispatcher(cfg, log)
	if err != nil {
		return err
	}
	defer d.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	enc := json.NewEncoder(os.Stdout)
	if err := d.Subscribe(ctx, *topic, func(ctx context.Context, msg dispatchnet.Message) error {
		return enc.Encode(map[string]any{
			"topic":    msg.Topic,
			"agent_id": msg.AgentID,
			"payload":  msg.Payload,
		})
	}); err != nil {
		return err
	}

	<-ctx.Done()
	return nil
}

func runHeartbeat(args []string, log *slog.Logger) error {
	fs, cfg := newFlagSet("heartbeat")
	interval := fs.Int("interval", 30, "heartbeat interval in seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *interval <= 0 {
		return errors.New("--interval must be greater than zero")
	}

	d, err := newDispatcher(cfg, log)
	if err != nil {
		return err
	}
	defer d.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(time.Duration(*interval) * time.Second)
	defer ticker.Stop()

	payload := map[string]any{"agent_id": cfg.agentID, "status": "healthy"}
	for {
		if err := d.Publish(ctx, dispatchnet.TopicHeartbeat, payload); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func newFlagSet(name string) (*flag.FlagSet, *config) {
	cfg := &config{
		natsURL:   envOr("NATS_URL", "nats://localhost:4222"),
		namespace: envOr("NAMESPACE", "agents.dispatch"),
		agentID:   envOr("AGENT_ID", defaultAgentID()),
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.natsURL, "nats-url", cfg.natsURL, "NATS server URL")
	fs.StringVar(&cfg.namespace, "namespace", cfg.namespace, "dispatch namespace")
	fs.StringVar(&cfg.agentID, "agent-id", cfg.agentID, "agent identifier")
	fs.BoolVar(&cfg.allowNoop, "allow-noop", false, "use noop dispatcher if NATS is unavailable")
	return fs, cfg
}

func newDispatcher(cfg *config, log *slog.Logger) (dispatchnet.Dispatcher, error) {
	d, err := dispatchnet.NewNATS(cfg.natsURL, cfg.agentID, cfg.namespace, log)
	if err == nil {
		return d, nil
	}
	if cfg.allowNoop {
		fmt.Fprintf(os.Stderr, "warning: NATS unavailable, using noop dispatcher: %v\n", err)
		return dispatchnet.NewNoop(), nil
	}
	return nil, fmt.Errorf("failed to connect to NATS at %s: %w", cfg.natsURL, err)
}

func parsePayload(text string) map[string]any {
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil || payload == nil {
		return map[string]any{"message": text}
	}
	return payload
}

func validTopic(topic string) bool {
	switch topic {
	case dispatchnet.TopicDispatch,
		dispatchnet.TopicClaim,
		dispatchnet.TopicResult,
		dispatchnet.TopicHeartbeat,
		dispatchnet.TopicComms:
		return true
	default:
		return false
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func defaultAgentID() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return "unknown"
	}
	return hostname
}

