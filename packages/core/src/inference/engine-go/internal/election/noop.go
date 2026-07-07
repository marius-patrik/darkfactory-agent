package election

import "context"

// NoopElection always reports as leader. Used for single-node dev mode.
type NoopElection struct {
	nodeID string
}

// NewNoop creates a no-op election that is always leader.
func NewNoop(nodeID string) *NoopElection {
	return &NoopElection{nodeID: nodeID}
}

// Run blocks until ctx is cancelled.
func (n *NoopElection) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// IsLeader always returns true.
func (n *NoopElection) IsLeader() bool { return true }

// NodeID returns the node identity.
func (n *NoopElection) NodeID() string { return n.nodeID }
