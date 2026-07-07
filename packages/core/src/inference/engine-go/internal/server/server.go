// Package server implements the HTTP API surface for the daemon.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/election"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/queue"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/internal/store"
	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

// Server exposes the daemon HTTP API.
type Server struct {
	addr    string
	version string
	queue   *queue.Queue
	store   *store.Store
	elec    election.Election
	log     *slog.Logger
	srv     *http.Server
}

// New creates a Server.
func New(addr, version string, q *queue.Queue, st *store.Store, elec election.Election, log *slog.Logger) *Server {
	return &Server{
		addr:    addr,
		version: version,
		queue:   q,
		store:   st,
		elec:    elec,
		log:     log.With("component", "server"),
	}
}

// Start begins serving HTTP.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /v1/runs", s.handleSubmit)
	mux.HandleFunc("GET /v1/runs", s.handleList)
	mux.HandleFunc("GET /v1/runs/{id}", s.handleGet)
	mux.HandleFunc("GET /v1/runs/{id}/logs", s.handleLogs)
	mux.HandleFunc("POST /v1/runs/{id}/cancel", s.handleCancel)

	s.srv = &http.Server{
		Addr:    s.addr,
		Handler: withLog(s.log, withCORS(mux)),
	}
	s.log.Info("http server starting", "addr", s.addr)
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("http server error", "err", err)
		}
	}()
	return nil
}

// Stop gracefully shuts down.
func (s *Server) Stop(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	counts, err := s.store.CountRunsByStatus(r.Context())
	if err != nil {
		s.log.Warn("health counts failed", "err", err)
		counts = map[contracts.RunStatus]int{}
	}
	resp := contracts.HealthResponse{
		Status:    "healthy",
		Version:   s.version,
		GitSHA:    os.Getenv("AGENTS_GIT_SHA"),
		ImageTag:  os.Getenv("AGENTS_IMAGE_TAG"),
		BuildTime: os.Getenv("AGENTS_BUILD_TIME"),
		NodeID:    healthNodeID(s.elec),
		Leader:    healthLeader(s.elec),
		Executor:  envDefault("AGENTS_EXECUTOR", "kubernetes"),
		Namespace: envDefault("AGENTS_K8S_NAMESPACE", "agents"),
		Peers: map[string]string{
			"nats": os.Getenv("NATS_URL"),
		},
		Timestamp: time.Now().UTC(),
		Capacity: contracts.Capacity{
			Max:       s.queue.Cap(),
			Running:   counts[contracts.RunStatusRunning],
			Queued:    counts[contracts.RunStatusQueued],
			Completed: terminalCount(counts),
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

func terminalCount(counts map[contracts.RunStatus]int) int {
	total := 0
	for status, count := range counts {
		if status.Terminal() {
			total += count
		}
	}
	return total
}

func healthNodeID(e election.Election) string {
	if e == nil {
		return os.Getenv("NODE_ID")
	}
	return e.NodeID()
}

func healthLeader(e election.Election) bool {
	return e == nil || e.IsLeader()
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var req contracts.SubmitRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	if req.Image == "" {
		http.Error(w, `{"error":"image is required"}`, http.StatusBadRequest)
		return
	}
	if s.elec != nil && !s.elec.IsLeader() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":   "not_leader",
			"node_id": s.elec.NodeID(),
		})
		return
	}

	id := uuid.Must(uuid.NewV7()).String()
	run := &contracts.Run{
		ID:        id,
		Image:     req.Image,
		Command:   req.Command,
		Env:       req.Env,
		Labels:    req.Labels,
		IssueRef:  req.IssueRef,
		BranchRef: req.BranchRef,
		PRRef:     req.PRRef,
		HeadSHA:   req.HeadSHA,
	}

	ctx := r.Context()
	if err := s.queue.Submit(ctx, run); err != nil {
		s.log.Error("submit failed", "err", err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.ListRuns(r.Context(), 100)
	if err != nil {
		s.log.Error("list failed", "err", err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(run.Logs))
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, getErr := s.store.GetRun(r.Context(), id)
	if getErr == nil && run.Status.Terminal() {
		writeJSON(w, http.StatusOK, map[string]string{"status": string(run.Status), "run_id": id, "state": "already-terminal"})
		return
	}
	if err := s.queue.Cancel(r.Context(), id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start),
		)
	})
}

