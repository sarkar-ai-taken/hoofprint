// Package controlplane holds the agent-facing side of hoofprint-api: the
// registry of connected agents, the gRPC stream handler, and the dispatcher
// that turns work requests into WorkItems on the right agent's stream
// (ADR-0004).
package controlplane

import (
	"context"
	"log/slog"
	"sync"
	"time"

	agentv1 "github.com/sarkar-ai-taken/hoofprint/internal/agentproto/hoofprint/agent/v1"
	"github.com/sarkar-ai-taken/hoofprint/internal/model"
	"github.com/sarkar-ai-taken/hoofprint/internal/store"
)

// session is one live agent connection.
type session struct {
	agent *model.Agent
	// send carries control messages to the stream's writer goroutine.
	send chan *agentv1.ControlMessage
	// done is closed (via close()) when the stream ends.
	done     chan struct{}
	doneOnce sync.Once

	mu            sync.Mutex
	lastHeartbeat time.Time
}

// close marks the session finished. Safe to call from multiple goroutines.
func (s *session) close() {
	s.doneOnce.Do(func() { close(s.done) })
}

func (s *session) touch(t time.Time) {
	s.mu.Lock()
	s.lastHeartbeat = t
	s.mu.Unlock()
}

func (s *session) last() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastHeartbeat
}

// Registry tracks connected agents and reaps the silent ones.
type Registry struct {
	store    store.Store
	log      *slog.Logger
	timeout  time.Duration
	interval time.Duration

	mu       sync.RWMutex
	sessions map[string]*session
}

// NewRegistry builds a registry. heartbeatInterval is advertised to agents;
// an agent is marked offline after offlineAfter of silence.
func NewRegistry(st store.Store, log *slog.Logger, heartbeatInterval, offlineAfter time.Duration) *Registry {
	if log == nil {
		log = slog.Default()
	}
	return &Registry{
		store:    st,
		log:      log,
		timeout:  offlineAfter,
		interval: heartbeatInterval,
		sessions: map[string]*session{},
	}
}

// add registers a session, replacing (and closing) any previous one for the
// same agent id.
func (r *Registry) add(s *session) {
	r.mu.Lock()
	old, had := r.sessions[s.agent.ID]
	r.sessions[s.agent.ID] = s
	r.mu.Unlock()
	if had {
		r.log.Warn("agent reconnected; closing stale session", "agent", s.agent.ID)
		old.close()
	}
}

// remove drops a session if it is still the current one for its agent, and
// reports whether it was. A false return means a newer session has replaced
// it, so the agent is still online.
func (r *Registry) remove(s *session) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.sessions[s.agent.ID]; ok && cur == s {
		delete(r.sessions, s.agent.ID)
		return true
	}
	return false
}

func (r *Registry) get(id string) (*session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[id]
	return s, ok
}

// Online returns the agents with a live stream, optionally filtered by tag
// selector and required capability.
func (r *Registry) Online(selector map[string]string, capability string) []*model.Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []*model.Agent{}
	for _, s := range r.sessions {
		if !s.agent.MatchesTags(selector) {
			continue
		}
		if capability != "" && !s.agent.HasCapability(capability) {
			continue
		}
		cp := *s.agent
		out = append(out, &cp)
	}
	return out
}

// Count returns the number of live sessions.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

// RunReaper marks agents offline after r.timeout of silence and closes their
// streams. It returns when ctx is cancelled.
func (r *Registry) RunReaper(ctx context.Context) {
	tick := time.NewTicker(r.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			r.reap(ctx, now)
		}
	}
}

func (r *Registry) reap(ctx context.Context, now time.Time) {
	r.mu.RLock()
	var stale []*session
	for _, s := range r.sessions {
		if now.Sub(s.last()) > r.timeout {
			stale = append(stale, s)
		}
	}
	r.mu.RUnlock()
	for _, s := range stale {
		r.log.Warn("agent heartbeat timed out; marking offline", "agent", s.agent.ID, "last", s.last())
		s.close()
		if err := r.store.SetAgentStatus(ctx, s.agent.ID, model.AgentOffline); err != nil {
			r.log.Error("set agent offline", "agent", s.agent.ID, "err", err)
		}
	}
}
