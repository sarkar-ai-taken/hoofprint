// Package memory is an in-memory Store for development and tests.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/sarkar-ai-taken/hoofprint/internal/model"
	"github.com/sarkar-ai-taken/hoofprint/internal/store"
)

type Store struct {
	mu       sync.RWMutex
	agents   map[string]*model.Agent
	deploys  map[string]*model.Deploy
	verdicts map[string]*model.Verdict
	evidence map[string][]*model.Evidence // by verdict id
	now      func() time.Time
}

var _ store.Store = (*Store)(nil)

func New() *Store {
	return &Store{
		agents:   map[string]*model.Agent{},
		deploys:  map[string]*model.Deploy{},
		verdicts: map[string]*model.Verdict{},
		evidence: map[string][]*model.Evidence{},
		now:      time.Now,
	}
}

func (s *Store) UpsertAgent(_ context.Context, a *model.Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *a
	if existing, ok := s.agents[a.ID]; ok && cp.RegisteredAt.IsZero() {
		cp.RegisteredAt = existing.RegisteredAt
	}
	if cp.RegisteredAt.IsZero() {
		cp.RegisteredAt = s.now()
	}
	s.agents[a.ID] = &cp
	return nil
}

func (s *Store) GetAgent(_ context.Context, id string) (*model.Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.agents[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (s *Store) ListAgents(_ context.Context) ([]*model.Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.Agent, 0, len(s.agents))
	for _, a := range s.agents {
		cp := *a
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) SetAgentStatus(_ context.Context, id string, status model.AgentStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok {
		return store.ErrNotFound
	}
	a.Status = status
	return nil
}

func (s *Store) TouchAgent(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok {
		return store.ErrNotFound
	}
	a.LastHeartbeat = s.now()
	a.Status = model.AgentOnline
	return nil
}

func (s *Store) InsertDeploy(_ context.Context, d *model.Deploy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *d
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = s.now()
	}
	s.deploys[d.ID] = &cp
	return nil
}

func (s *Store) GetDeploy(_ context.Context, id string) (*model.Deploy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.deploys[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *d
	return &cp, nil
}

func (s *Store) ListDeploys(_ context.Context, f model.DeployFilter) ([]*model.Deploy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.Deploy, 0, len(s.deploys))
	for _, d := range s.deploys {
		if f.Cluster != "" && d.Cluster != f.Cluster {
			continue
		}
		if f.Namespace != "" && d.Namespace != f.Namespace {
			continue
		}
		cp := *d
		out = append(out, &cp)
	}
	// Newest first; stable tiebreak on id so tests are deterministic.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ObservedAt.Equal(out[j].ObservedAt) {
			return out[i].ObservedAt.After(out[j].ObservedAt)
		}
		return out[i].ID > out[j].ID
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (s *Store) InsertVerdict(_ context.Context, v *model.Verdict, evidence []*model.Evidence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.deploys[v.DeployID]; !ok {
		return store.ErrNotFound
	}
	cp := *v
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = s.now()
	}
	s.verdicts[v.ID] = &cp
	evs := make([]*model.Evidence, 0, len(evidence))
	for _, e := range evidence {
		ecp := *e
		ecp.VerdictID = v.ID
		if ecp.CreatedAt.IsZero() {
			ecp.CreatedAt = cp.CreatedAt
		}
		evs = append(evs, &ecp)
	}
	s.evidence[v.ID] = evs
	return nil
}

func (s *Store) GetVerdict(_ context.Context, id string) (*model.Verdict, []*model.Evidence, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.verdicts[id]
	if !ok {
		return nil, nil, store.ErrNotFound
	}
	cp := *v
	evs := make([]*model.Evidence, 0, len(s.evidence[id]))
	for _, e := range s.evidence[id] {
		ecp := *e
		evs = append(evs, &ecp)
	}
	return &cp, evs, nil
}

func (s *Store) ListVerdicts(_ context.Context, deployID string) ([]*model.Verdict, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*model.Verdict{}
	for _, v := range s.verdicts {
		if deployID != "" && v.DeployID != deployID {
			continue
		}
		cp := *v
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (s *Store) Close() error { return nil }
