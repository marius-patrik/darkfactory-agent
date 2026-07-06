package election

import "context"

type Noop struct {
	nodeID string
}

func NewNoop(nodeID string) *Noop { return &Noop{nodeID: nodeID} }
func (n *Noop) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
func (n *Noop) IsLeader() bool { return true }
func (n *Noop) NodeID() string { return n.nodeID }
