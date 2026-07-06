// Package runner defines the swappable run executor interface.
package runner

import "context"

// Interface abstracts container or CI-based run execution.
type Interface interface {
	Start(ctx context.Context, image string, cmd []string, env, labels map[string]string) (string, error)
	Stop(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error
	IsRunning(ctx context.Context, id string) (bool, error)
	ExitCode(ctx context.Context, id string) (int, error)
	Logs(ctx context.Context, id string) (string, error)
	URL(ctx context.Context, id string) (string, error)
	Close() error
}
