package natslane

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/marius-patrik/agentos/inference-engine/services/daemon/internal/ops"
	"github.com/marius-patrik/agentos/inference-engine/services/daemon/internal/toolexec"
)

type countingExec struct {
	calls int
}

func (c *countingExec) Run(ctx context.Context, name string, args map[string]any) toolexec.Result {
	c.calls++
	return toolexec.Result{Output: "ran " + name}
}

func TestToolCallToResult(t *testing.T) {
	exec := &countingExec{}
	lane := New(exec, ops.NewBroker(ops.NewMemStore()), nil)
	wire, _ := json.Marshal(ToolCall{CallID: "c1", Name: "read", Args: map[string]any{"path": "x"}, Host: "h1"})
	resp, err := lane.Handle(context.Background(), wire)
	if err != nil {
		t.Fatal(err)
	}
	var got ToolResult
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatal(err)
	}
	if got.CallID != "c1" || got.Output != "ran read" || got.IsError {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestRedeliveryDoesNotReexecute(t *testing.T) {
	root := t.TempDir()
	exec, err := toolexec.New(root)
	if err != nil {
		t.Fatal(err)
	}
	lane := New(exec, ops.NewBroker(ops.NewMemStore()), nil)
	call := ToolCall{CallID: "same", Name: "bash", Args: map[string]any{"command": "echo hit >> side_effect.txt"}}
	wire, _ := json.Marshal(call)
	if _, err := lane.Handle(context.Background(), wire); err != nil {
		t.Fatal(err)
	}
	if _, err := lane.Handle(context.Background(), wire); err != nil {
		t.Fatal(err)
	}
	read := exec.Run(context.Background(), "read", map[string]any{"path": "side_effect.txt"})
	if read.Error {
		t.Fatalf("read side effect: %s", read.Output)
	}
	if strings.Count(read.Output, "hit") != 1 {
		t.Fatalf("side effect output = %q, want one hit", read.Output)
	}
}

