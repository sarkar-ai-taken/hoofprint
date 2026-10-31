// Package agent is the hoofprint-agent runtime: one outbound gRPC stream to
// the control plane, heartbeats, work execution, and relay of DeployEvents
// from the watcher (ADR-0004).
package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/sarkar-ai-taken/hoofprint/internal/agentproto/hoofprint/agent/v1"
)

// Config is the agent's static configuration.
type Config struct {
	ControlPlane string // host:port
	Token        string
	AgentID      string
	Cluster      string
	Version      string
	Capabilities []string
	Tags         map[string]string
	// Insecure disables TLS (dev / in-cluster quickstart).
	Insecure bool
	// TLSServerName overrides the expected server name on TLS.
	TLSServerName string
	// HeartbeatInterval is the default until the control plane says otherwise.
	HeartbeatInterval time.Duration
	Log               *slog.Logger
}

// Handler executes one kind of WorkItem. logf streams a log line back to the
// control plane while the work runs.
type Handler func(ctx context.Context, payload json.RawMessage, logf func(string)) (json.RawMessage, error)

// Agent is the runtime.
type Agent struct {
	cfg      Config
	log      *slog.Logger
	handlers map[string]Handler

	// outbox carries messages from any goroutine to the single stream writer.
	outbox chan *agentv1.AgentMessage

	mu        sync.RWMutex
	connected bool
}

// New builds an Agent with no handlers; register them with Handle.
func New(cfg Config) *Agent {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 10 * time.Second
	}
	if cfg.AgentID == "" {
		cfg.AgentID = "agent-" + cfg.Cluster
	}
	return &Agent{cfg: cfg, log: cfg.Log, handlers: map[string]Handler{}, outbox: make(chan *agentv1.AgentMessage, 256)}
}

// Handle registers a handler for a work kind.
func (a *Agent) Handle(kind string, h Handler) { a.handlers[kind] = h }

// EmitDeploy queues a DeployEvent for the control plane. If the agent is
// disconnected the event waits in the outbox and is sent on reconnect; if the
// outbox is full the oldest event is dropped and logged.
func (a *Agent) EmitDeploy(ev *agentv1.DeployEvent) {
	a.enqueue(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_DeployEvent{DeployEvent: ev}})
}

func (a *Agent) enqueue(m *agentv1.AgentMessage) {
	for {
		select {
		case a.outbox <- m:
			return
		default:
			select {
			case dropped := <-a.outbox:
				a.log.Warn("agent outbox full; dropping oldest message", "type", fmt.Sprintf("%T", dropped.Msg))
			default:
			}
		}
	}
}

// Connected reports whether a stream is currently open and registered.
func (a *Agent) Connected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
}

func (a *Agent) setConnected(v bool) {
	a.mu.Lock()
	a.connected = v
	a.mu.Unlock()
}

// Run connects and reconnects with backoff until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		start := time.Now()
		err := a.runOnce(ctx)
		a.setConnected(false)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		jitter := time.Duration(rand.Int63n(int64(backoff) / 2))
		a.log.Warn("control plane stream ended; reconnecting", "err", err, "in", backoff+jitter)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff + jitter):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) dial() (*grpc.ClientConn, error) {
	var creds credentials.TransportCredentials
	if a.cfg.Insecure {
		creds = insecure.NewCredentials()
	} else {
		creds = credentials.NewTLS(&tls.Config{ServerName: a.cfg.TLSServerName, MinVersion: tls.VersionTLS12})
	}
	return grpc.NewClient(a.cfg.ControlPlane, grpc.WithTransportCredentials(creds))
}

func (a *Agent) runOnce(ctx context.Context) error {
	conn, err := a.dial()
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if a.cfg.Token != "" {
		sctx = metadata.AppendToOutgoingContext(sctx, "authorization", "Bearer "+a.cfg.Token)
	}
	stream, err := agentv1.NewAgentServiceClient(conn).Connect(sctx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	if err := stream.Send(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_Register{Register: &agentv1.Register{
		AgentId: a.cfg.AgentID, Cluster: a.cfg.Cluster, Version: a.cfg.Version,
		Capabilities: a.cfg.Capabilities, Tags: a.cfg.Tags,
	}}}); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("await registration: %w", err)
	}
	reg := first.GetRegistered()
	if reg == nil {
		return errors.New("expected Registered as first message")
	}
	interval := a.cfg.HeartbeatInterval
	if reg.HeartbeatIntervalSeconds > 0 {
		interval = time.Duration(reg.HeartbeatIntervalSeconds) * time.Second
	}
	a.setConnected(true)
	a.log.Info("registered with control plane", "agent", a.cfg.AgentID, "cluster", a.cfg.Cluster, "heartbeat", interval, "capabilities", a.cfg.Capabilities)

	errc := make(chan error, 3)

	// Writer: sole caller of stream.Send.
	go func() {
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-sctx.Done():
				errc <- sctx.Err()
				return
			case <-tick.C:
				if err := stream.Send(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_Heartbeat{Heartbeat: &agentv1.Heartbeat{SentAt: timestamppb.Now()}}}); err != nil {
					errc <- fmt.Errorf("send heartbeat: %w", err)
					return
				}
			case m := <-a.outbox:
				if err := stream.Send(m); err != nil {
					// Put it back so it survives the reconnect.
					a.enqueue(m)
					errc <- fmt.Errorf("send: %w", err)
					return
				}
			}
		}
	}()

	// Reader.
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				errc <- fmt.Errorf("recv: %w", err)
				return
			}
			switch m := msg.Msg.(type) {
			case *agentv1.ControlMessage_WorkItem:
				go a.runWork(sctx, m.WorkItem)
			case *agentv1.ControlMessage_Ping:
				a.enqueue(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_Heartbeat{Heartbeat: &agentv1.Heartbeat{SentAt: timestamppb.Now()}}})
			}
		}
	}()

	return <-errc
}

func (a *Agent) runWork(ctx context.Context, wi *agentv1.WorkItem) {
	a.enqueue(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_WorkAck{WorkAck: &agentv1.WorkAck{WorkId: wi.Id}}})
	logf := func(line string) {
		a.enqueue(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_WorkEvent{WorkEvent: &agentv1.WorkEvent{
			WorkId: wi.Id, Event: &agentv1.WorkEvent_Log{Log: &agentv1.LogLine{At: timestamppb.Now(), Line: line}},
		}}})
	}
	result := &agentv1.WorkResult{}
	h, ok := a.handlers[wi.Kind]
	if !ok {
		result.Error = fmt.Sprintf("unknown work kind %q", wi.Kind)
	} else {
		start := time.Now()
		a.log.Info("work started", "id", wi.Id, "kind", wi.Kind)
		out, err := h(ctx, wi.Payload, logf)
		if err != nil {
			result.Error = err.Error()
			a.log.Warn("work failed", "id", wi.Id, "kind", wi.Kind, "err", err, "dur", time.Since(start))
		} else {
			result.Ok = true
			result.Payload = out
			a.log.Info("work done", "id", wi.Id, "kind", wi.Kind, "dur", time.Since(start))
		}
	}
	a.enqueue(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_WorkEvent{WorkEvent: &agentv1.WorkEvent{
		WorkId: wi.Id, Event: &agentv1.WorkEvent_Result{Result: result},
	}}})
}
