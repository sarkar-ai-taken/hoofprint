// Package store defines persistence for the control plane. Two
// implementations exist: postgres (production) and memory (dev, tests).
package store

import (
	"context"
	"errors"

	"github.com/sarkar-ai-taken/hoofprint/internal/model"
)

// ErrNotFound is returned when a lookup by id finds nothing.
var ErrNotFound = errors.New("not found")

// Store is the persistence boundary for the control plane.
type Store interface {
	UpsertAgent(ctx context.Context, a *model.Agent) error
	GetAgent(ctx context.Context, id string) (*model.Agent, error)
	ListAgents(ctx context.Context) ([]*model.Agent, error)
	// SetAgentStatus updates liveness without touching registration fields.
	SetAgentStatus(ctx context.Context, id string, status model.AgentStatus) error
	// TouchAgent records a heartbeat.
	TouchAgent(ctx context.Context, id string) error

	InsertDeploy(ctx context.Context, d *model.Deploy) error
	GetDeploy(ctx context.Context, id string) (*model.Deploy, error)
	ListDeploys(ctx context.Context, f model.DeployFilter) ([]*model.Deploy, error)

	InsertVerdict(ctx context.Context, v *model.Verdict, evidence []*model.Evidence) error
	GetVerdict(ctx context.Context, id string) (*model.Verdict, []*model.Evidence, error)
	ListVerdicts(ctx context.Context, deployID string) ([]*model.Verdict, error)

	Close() error
}
