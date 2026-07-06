// Package natslane carries ToolCall requests over NATS and returns ToolResult responses.
package natslane

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/marius-patrik/agentos/inference-engine/services/daemon/internal/ops"
	"github.com/marius-patrik/agentos/inference-engine/services/daemon/internal/toolexec"
)

type ToolCall struct {
	CallID   string         `json:"call_id"`
	Name     string         `json:"name"`
	Args     map[string]any `json:"args"`
	Host     string         `json:"host"`
	WorkerID string         `json:"worker_id"`
}

type ToolResult struct {
	CallID      string `json:"call_id"`
	Output      string `json:"output"`
	IsError     bool   `json:"is_error"`
	ArtifactRef string `json:"artifact_ref,omitempty"`
}

type Executor interface {
	Run(context.Context, string, map[string]any) toolexec.Result
}

type Lane struct {
	exec   Executor
	broker *ops.Broker
	log    *slog.Logger
}

func New(exec Executor, broker *ops.Broker, log *slog.Logger) *Lane {
	if log == nil {
		log = slog.Default()
	}
	return &Lane{exec: exec, broker: broker, log: log.With("component", "natslane")}
}

func (l *Lane) Handle(ctx context.Context, data []byte) ([]byte, error) {
	var call ToolCall
	if err := json.Unmarshal(data, &call); err != nil {
		return nil, err
	}
	start := time.Now()
	payload := string(data)
	op := ops.NewEnvelope("tool_call", call.CallID, "execute", payload)
	raw, err := l.broker.DoResult(ctx, op, func(ctx context.Context) (string, error) {
		result := l.exec.Run(ctx, call.Name, call.Args)
		wire, err := json.Marshal(ToolResult{CallID: call.CallID, Output: result.Output, IsError: result.Error})
		if err != nil {
			return "", err
		}
		return string(wire), nil
	})
	if err != nil {
		return nil, err
	}
	var result ToolResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, err
	}
	l.log.Info("tool call handled", "call_id", call.CallID, "tool", call.Name, "duration_ms", time.Since(start).Milliseconds(), "is_error", result.IsError)
	return []byte(raw), nil
}

func (l *Lane) Subscribe(ctx context.Context, nc *nats.Conn, subject string) (*nats.Subscription, error) {
	return nc.Subscribe(subject, func(msg *nats.Msg) {
		result, err := l.Handle(ctx, msg.Data)
		if err != nil {
			l.log.Warn("tool call failed", "err", err)
			return
		}
		if msg.Reply != "" {
			if err := msg.Respond(result); err != nil {
				l.log.Warn("tool response failed", "err", err)
			}
			return
		}
		var tr ToolResult
		if err := json.Unmarshal(result, &tr); err != nil {
			l.log.Warn("tool result decode failed", "err", err)
			return
		}
		if err := nc.Publish("rommie.exec.result."+tr.CallID, result); err != nil {
			l.log.Warn("tool result publish failed", "call_id", tr.CallID, "err", err)
		}
	})
}

