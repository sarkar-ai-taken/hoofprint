package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	agentv1 "github.com/sarkar-ai-taken/hoofprint/internal/agentproto/hoofprint/agent/v1"
)

// Errors returned by the dispatcher.
var (
	ErrNoAgent           = errors.New("no online agent matches the request")
	ErrAgentDisconnected = errors.New("agent disconnected before completing the work")
)

// WorkRequest asks for a WorkItem to run on one agent. Set AgentID to target a
// specific agent, or Selector (+ optional Capability) to let the dispatcher
// pick one.
type WorkRequest struct {
	AgentID    string
	Selector   map[string]string
	Capability string
	Kind       string
	Payload    json.RawMessage
}

// WorkResult is the terminal outcome of a WorkItem plus any log lines the
// agent streamed while running it.
type WorkResult struct {
	WorkID  string          `json:"work_id"`
	AgentID string          `json:"agent_id"`
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Logs    []string        `json:"logs,omitempty"`
}

// Executor is the only way the control plane runs anything (ADR-0004). The
// single implementation dispatches to agents; there is no local executor.
type Executor interface {
	Execute(ctx context.Context, req WorkRequest) (*WorkResult, error)
}

type pending struct {
	agentID string
	result  chan *WorkResult
	mu      sync.Mutex
	logs    []string
}

// Dispatcher implements Executor over the Registry's sessions.
type Dispatcher struct {
	reg *Registry

	mu      sync.Mutex
	pending map[string]*pending
}

var _ Executor = (*Dispatcher)(nil)

func NewDispatcher(reg *Registry) *Dispatcher {
	return &Dispatcher{reg: reg, pending: map[string]*pending{}}
}

// Execute sends the work item and blocks until the agent reports a result,
// ctx ends, or the agent's stream closes.
func (d *Dispatcher) Execute(ctx context.Context, req WorkRequest) (*WorkResult, error) {
	s, err := d.pick(req)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	p := &pending{agentID: s.agent.ID, result: make(chan *WorkResult, 1)}
	d.mu.Lock()
	d.pending[id] = p
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.pending, id)
		d.mu.Unlock()
	}()

	msg := &agentv1.ControlMessage{Msg: &agentv1.ControlMessage_WorkItem{WorkItem: &agentv1.WorkItem{
		Id: id, Kind: req.Kind, Payload: req.Payload,
	}}}
	select {
	case s.send <- msg:
	case <-s.done:
		return nil, ErrAgentDisconnected
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case res := <-p.result:
		return res, nil
	case <-s.done:
		return nil, ErrAgentDisconnected
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (d *Dispatcher) pick(req WorkRequest) (*session, error) {
	if req.AgentID != "" {
		s, ok := d.reg.get(req.AgentID)
		if !ok {
			return nil, fmt.Errorf("%w: agent %q not connected", ErrNoAgent, req.AgentID)
		}
		return s, nil
	}
	d.reg.mu.RLock()
	defer d.reg.mu.RUnlock()
	for _, s := range d.reg.sessions {
		if s.agent.MatchesTags(req.Selector) && (req.Capability == "" || s.agent.HasCapability(req.Capability)) {
			return s, nil
		}
	}
	return nil, ErrNoAgent
}

// onEvent routes a WorkEvent from an agent to the waiting Execute call.
func (d *Dispatcher) onEvent(agentID string, ev *agentv1.WorkEvent) {
	d.mu.Lock()
	p, ok := d.pending[ev.WorkId]
	d.mu.Unlock()
	if !ok || p.agentID != agentID {
		return
	}
	switch e := ev.Event.(type) {
	case *agentv1.WorkEvent_Log:
		p.mu.Lock()
		p.logs = append(p.logs, e.Log.Line)
		p.mu.Unlock()
	case *agentv1.WorkEvent_Status:
		p.mu.Lock()
		p.logs = append(p.logs, fmt.Sprintf("[%s] %s", e.Status.Phase, e.Status.Message))
		p.mu.Unlock()
	case *agentv1.WorkEvent_Result:
		p.mu.Lock()
		logs := p.logs
		p.mu.Unlock()
		res := &WorkResult{WorkID: ev.WorkId, AgentID: agentID, OK: e.Result.Ok, Error: e.Result.Error, Payload: e.Result.Payload, Logs: logs}
		select {
		case p.result <- res:
		default:
		}
	}
}
