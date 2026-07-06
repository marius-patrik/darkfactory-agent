// Manager is the single global manager for the Agents platform.
//
// It watches GitHub issues, classifies them by label (PRD/ADR/log/suggestion),
// decomposes PRDs into sub-task runs via the gateway LLM, and submits each
// run to the daemon's HTTP API. It maintains the run invariant:
// each run == one branch + one draft PR + one log issue/comment.
//
// Every side-effecting operation uses an OperationEnvelope + idempotency key.
//
// HA MODE: When NATS is configured, the manager participates in leader
// election. Only the active leader runs the polling loop.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/election"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/ghauth"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/ops"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/manager"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := manager.FromFile("manager.json")
	if err != nil {
		log.Error("config load failed", "err", err)
		os.Exit(1)
	}
	log.Info("manager starting", "version", cfg.Version, "repo", cfg.FullRepo(), "poll", cfg.PollInterval, "node_id", cfg.NodeID)

	st, err := store.New(cfg.DBPath)
	if err != nil {
		log.Error("store open failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	stateStore, err := manager.NewStateStore(cfg.DBPath + "_state")
	if err != nil {
		log.Error("state store open failed", "err", err)
		os.Exit(1)
	}
	defer stateStore.Close()

	broker := ops.NewBroker(st)

	// Initialize App-token provider for automatic Bearer injection.
	tokenProvider, err := ghauth.NewDefaultClient()
	if err != nil {
		log.Error("ghauth init failed", "err", err)
		os.Exit(1)
	}

	gh := manager.NewGHRESTClient(cfg.FullRepo(), tokenProvider)
	gw := manager.NewGatewayClient(cfg.GatewayURL)
	daemon := manager.NewDaemonClientMulti(cfg.DaemonURLs)

	o := manager.NewManager(cfg, log, gh, gw, daemon, stateStore, broker, tokenProvider)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Leader election: noop only in explicit single-node mode (no NATS). When NATS
	// is configured this node MUST participate in election and fail closed — it
	// must never silently fall back to always-leader, which causes split-brain
	// (this mirrors cmd/daemon/main.go exactly).
	var elec election.Election = election.NewNoop(cfg.NodeID)
	if cfg.NATSURL != "" {
		if cfg.NodeID == "" {
			log.Error("nats election requires node_id when nats_url is configured")
			os.Exit(1)
		}
		natsElec, err := election.NewNATS(cfg.NATSURL, cfg.NodeID, log, election.WithKey("manager"))
		if err != nil {
			log.Error("election init failed", "err", err)
			os.Exit(1)
		}
		elec = natsElec
		defer natsElec.Close()
		log.Info("election configured", "node_id", cfg.NodeID)
	}

	go elec.Run(ctx)

	// Wire leadership check into manager ticks.
	o.SetLeaderCheck(elec.IsLeader)

	if os.Getenv("AGENTS_MANAGER_ONCE") == "1" {
		issueNumber := 0
		if raw := os.Getenv("AGENTS_MANAGER_ISSUE"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				log.Error("invalid AGENTS_MANAGER_ISSUE", "value", raw)
				os.Exit(2)
			}
			issueNumber = n
		}
		if err := o.RunOnce(ctx, issueNumber); err != nil {
			log.Error("one-shot tick failed", "err", err, "issue", issueNumber)
			os.Exit(1)
		}
		log.Info("one-shot tick complete", "issue", issueNumber)
		return
	}

	// Start HTTP health endpoint regardless of leadership.
	if err := o.Start(ctx); err != nil {
		log.Error("start failed", "err", err)
		os.Exit(1)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = o.Stop(shutdown)
	}()

	log.Info("manager ready", "addr", cfg.ListenAddr, "node_id", cfg.NodeID)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Info("shutting down", "signal", sig.String())
}

