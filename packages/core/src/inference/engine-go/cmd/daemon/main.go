// Daemon is the Go manager daemon for the Agents platform.
//
// It runs agent work as Kubernetes Jobs with bounded concurrency,
// persists an idempotent operation log, emits lifecycle events, and exposes
// an HTTP API for run submission and status.
//
// HA MODE: When NATS is configured, the daemon participates in leader
// election. Only the active leader runs the queue scheduler; standbys
// keep the HTTP API alive in degraded mode.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/config"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/docker"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/election"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/events"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/ghauth"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/githubactions"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/kubernetes"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/ops"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/queue"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/runner"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/server"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.FromFile("daemon.json")
	if err != nil {
		log.Error("config load failed", "err", err)
		os.Exit(1)
	}
	log.Info("daemon starting", "version", cfg.Version, "addr", cfg.ListenAddr, "cap", cfg.ConcurrencyCap, "node_id", cfg.NodeID)

	st, err := store.New(cfg.DBPath)
	if err != nil {
		log.Error("store open failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	var r runner.Interface
	switch cfg.Executor {
	case "kubernetes", "k8s":
		r = kubernetes.NewRunner(kubernetes.Config{
			Namespace:           cfg.KubernetesNamespace,
			Kubeconfig:          cfg.Kubeconfig,
			CPURequest:          cfg.K8SCPURequest,
			MemoryRequest:       cfg.K8SMemoryRequest,
			CPULimit:            cfg.K8SCPULimit,
			MemoryLimit:         cfg.K8SMemoryLimit,
			GPULimit:            cfg.K8SGPULimit,
			TopologySpread:      cfg.K8STopologySpread,
			AvoidDiskPressure:   cfg.K8SAvoidDiskPressure,
			DisallowedNodeNames: splitCSV(cfg.K8SDisallowedNodes),
		}, log)
		log.Info("kubernetes executor configured", "namespace", cfg.KubernetesNamespace)
	case "github-actions":
		if cfg.GitHubOwner == "" || cfg.GitHubRepo == "" {
			log.Error("github-actions executor requires github_owner and github_repo")
			os.Exit(1)
		}

		var tokenProvider ghauth.TokenProvider
		if cfg.GitHubToken != "" {
			// Static token fallback (legacy / local dev).
			tokenProvider = ghauth.StaticToken(cfg.GitHubToken)
			log.Info("github-actions using static token")
		} else {
			// Dynamic App installation token with auto-refresh.
			var err error
			tokenProvider, err = ghauth.NewDefaultClient()
			if err != nil {
				log.Error("github-actions app-auth init failed", "err", err)
				os.Exit(1)
			}
			log.Info("github-actions using app-token provider")
		}

		r = githubactions.NewRunner(tokenProvider, cfg.GitHubOwner, cfg.GitHubRepo, cfg.GitHubWorkflow, cfg.GitHubRef, log)
		log.Info("github-actions executor configured", "owner", cfg.GitHubOwner, "repo", cfg.GitHubRepo)
	default:
		if cfg.DockerEnabled {
			dm, err := docker.NewManager()
			if err != nil {
				log.Error("docker connect failed", "err", err)
				os.Exit(1)
			}
			defer dm.Close()
			if err := dm.Ping(context.Background()); err != nil {
				log.Error("docker ping failed", "err", err)
				os.Exit(1)
			}
			log.Info("docker connected")
			r = dm
		} else {
			log.Error("no executor configured and docker disabled")
			os.Exit(1)
		}
	}

	var bus events.Bus = events.NewNoop()
	if cfg.NATSURL != "" {
		natsBus, err := events.NewNATS(cfg.NATSURL, cfg.NATSTopic, log)
		if err != nil {
			log.Error("nats connect failed", "err", err)
			os.Exit(1)
		} else {
			bus = natsBus
			defer natsBus.Close()
			log.Info("nats connected", "url", cfg.NATSURL)
		}
	}

	// Leader election: noop when no NATS, NATS-backed when configured.
	var elec election.Election = election.NewNoop(cfg.NodeID)
	if cfg.NATSURL != "" {
		if cfg.NodeID == "" {
			log.Error("nats election requires node_id when nats_url is configured")
			os.Exit(1)
		}
		natsElec, err := election.NewNATS(cfg.NATSURL, cfg.NodeID, log, election.WithKey("daemon"))
		if err != nil {
			log.Error("election init failed", "err", err)
			os.Exit(1)
		}
		elec = natsElec
		defer natsElec.Close()
		log.Info("election configured", "node_id", cfg.NodeID)
	}

	broker := ops.NewBroker(st)
	q := queue.New(cfg.ConcurrencyCap, st, r, bus, broker, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start election loop in background.
	go elec.Run(ctx)

	// Wait for leadership before starting the queue scheduler.
	// The HTTP server starts immediately so standbys can report health.
	srv := server.New(cfg.ListenAddr, cfg.Version, q, st, elec, log)
	if err := srv.Start(ctx); err != nil {
		log.Error("server start failed", "err", err)
		os.Exit(1)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Stop(shutdown)
	}()

	// Leadership polling loop.
	leaderCtx, leaderCancel := context.WithCancel(ctx)
	defer leaderCancel()
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		active := false
		for {
			select {
			case <-ctx.Done():
				if active {
					q.Stop()
				}
				return
			case <-ticker.C:
				if elec.IsLeader() && !active {
					log.Info("became leader, starting queue")
					q.Start(leaderCtx)
					active = true
				} else if !elec.IsLeader() && active {
					log.Info("lost leadership, stopping queue")
					q.Stop()
					active = false
				}
			}
		}
	}()

	log.Info("daemon ready", "addr", cfg.ListenAddr, "node_id", cfg.NodeID)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Info("shutting down", "signal", sig.String())
}

func splitCSV(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

