package controlplane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/sarkar-ai-taken/hoofprint/internal/agentproto/hoofprint/agent/v1"
	"github.com/sarkar-ai-taken/hoofprint/internal/model"
	"github.com/sarkar-ai-taken/hoofprint/internal/store"
)

// DeployHandler is called for every DeployEvent after it is persisted. The
// verification pipeline hangs off this.
type DeployHandler func(ctx context.Context, d *model.Deploy)

// Server implements the AgentService gRPC API.
type Server struct {
	agentv1.UnimplementedAgentServiceServer

	store      store.Store
	reg        *Registry
	disp       *Dispatcher
	log        *slog.Logger
	agentToken string
	onDeploy   DeployHandler
}

// Option configures a Server.
type Option func(*Server)

// WithAgentToken requires agents to present this bearer token. Empty disables
// authentication (dev only; logged loudly).
func WithAgentToken(tok string) Option { return func(s *Server) { s.agentToken = tok } }

// WithDeployHandler registers the callback for persisted deploy events.
func WithDeployHandler(h DeployHandler) Option { return func(s *Server) { s.onDeploy = h } }

func NewServer(st store.Store, reg *Registry, disp *Dispatcher, log *slog.Logger, opts ...Option) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{store: st, reg: reg, disp: disp, log: log}
	for _, o := range opts {
		o(s)
	}
	if s.agentToken == "" {
		s.log.Warn("agent authentication disabled: no --agent-token configured")
	}
	return s
}

// Register attaches the service to a gRPC server.
func (s *Server) Register(g *grpc.Server) { agentv1.RegisterAgentServiceServer(g, s) }

func (s *Server) authenticate(ctx context.Context) error {
	if s.agentToken == "" {
		return nil
	}
	md, _ := metadata.FromIncomingContext(ctx)
	for _, v := range md.Get("authorization") {
		if strings.TrimPrefix(v, "Bearer ") == s.agentToken {
			return nil
		}
	}
	return status.Error(codes.Unauthenticated, "invalid agent token")
}

// Connect is the agent's single long-lived stream.
func (s *Server) Connect(stream agentv1.AgentService_ConnectServer) error {
	ctx := stream.Context()
	if err := s.authenticate(ctx); err != nil {
		return err
	}

	first, err := stream.Recv()
	if err != nil {
		return err
	}
	reg := first.GetRegister()
	if reg == nil {
		return status.Error(codes.InvalidArgument, "first message must be Register")
	}
	if reg.AgentId == "" || reg.Cluster == "" {
		return status.Error(codes.InvalidArgument, "agent_id and cluster are required")
	}

	now := time.Now().UTC()
	agent := &model.Agent{
		ID: reg.AgentId, Cluster: reg.Cluster, Version: reg.Version,
		Capabilities: append([]string{}, reg.Capabilities...), Tags: reg.Tags,
		Status: model.AgentOnline, LastHeartbeat: now,
	}
	if agent.Tags == nil {
		agent.Tags = map[string]string{}
	}
	if err := s.store.UpsertAgent(ctx, agent); err != nil {
		s.log.Error("upsert agent", "agent", agent.ID, "err", err)
		return status.Error(codes.Internal, "persist agent")
	}

	sess := &session{agent: agent, send: make(chan *agentv1.ControlMessage, 64), done: make(chan struct{}), lastHeartbeat: now}
	s.reg.add(sess)
	s.log.Info("agent connected", "agent", agent.ID, "cluster", agent.Cluster, "version", agent.Version, "capabilities", agent.Capabilities, "tags", agent.Tags)

	defer func() {
		wasCurrent := s.reg.remove(sess)
		sess.close()
		if !wasCurrent {
			// A newer session for this agent is live; leave its status alone.
			s.log.Info("stale agent session closed", "agent", agent.ID)
			return
		}
		bg, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.store.SetAgentStatus(bg, agent.ID, model.AgentOffline); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.log.Error("set agent offline", "agent", agent.ID, "err", err)
		}
		s.log.Info("agent disconnected", "agent", agent.ID)
	}()

	// Writer goroutine: the only caller of stream.Send.
	sendErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-sess.done:
				sendErr <- nil
				return
			case <-ctx.Done():
				sendErr <- ctx.Err()
				return
			case m := <-sess.send:
				if err := stream.Send(m); err != nil {
					sendErr <- err
					return
				}
			}
		}
	}()

	sess.send <- &agentv1.ControlMessage{Msg: &agentv1.ControlMessage_Registered{Registered: &agentv1.Registered{
		AgentId: agent.ID, HeartbeatIntervalSeconds: int32(s.reg.interval / time.Second),
	}}}

	// Reader loop.
	recvErr := make(chan error, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			s.handle(ctx, sess, msg)
		}
	}()

	select {
	case <-sess.done:
		// Replaced by a newer session or reaped.
		return status.Error(codes.Aborted, "session closed by control plane")
	case err := <-sendErr:
		return err
	case err := <-recvErr:
		if errors.Is(err, io.EOF) || status.Code(err) == codes.Canceled {
			return nil
		}
		return err
	}
}

func (s *Server) handle(ctx context.Context, sess *session, msg *agentv1.AgentMessage) {
	switch m := msg.Msg.(type) {
	case *agentv1.AgentMessage_Heartbeat:
		now := time.Now().UTC()
		sess.touch(now)
		if err := s.store.TouchAgent(ctx, sess.agent.ID); err != nil {
			s.log.Error("touch agent", "agent", sess.agent.ID, "err", err)
		}
	case *agentv1.AgentMessage_WorkAck:
		// Acks are informational in M0; the dispatcher waits for the result.
	case *agentv1.AgentMessage_WorkEvent:
		sess.touch(time.Now().UTC())
		s.disp.onEvent(sess.agent.ID, m.WorkEvent)
	case *agentv1.AgentMessage_DeployEvent:
		sess.touch(time.Now().UTC())
		d := deployFromProto(sess.agent, m.DeployEvent)
		if err := s.store.InsertDeploy(ctx, d); err != nil {
			s.log.Error("insert deploy", "deploy", d.ID, "err", err)
			return
		}
		s.log.Info("deploy observed", "deploy", d.ID, "cluster", d.Cluster, "ns", d.Namespace, "kind", d.Kind, "name", d.Name, "tier", d.Tier.String(), "rev", d.RevisionBefore+".."+d.RevisionAfter)
		if s.onDeploy != nil {
			go s.onDeploy(context.WithoutCancel(ctx), d)
		}
	case *agentv1.AgentMessage_Register:
		s.log.Warn("duplicate Register on open stream ignored", "agent", sess.agent.ID)
	}
}

func deployFromProto(agent *model.Agent, ev *agentv1.DeployEvent) *model.Deploy {
	id := ev.Id
	if id == "" {
		id = uuid.NewString()
	}
	observed := time.Now().UTC()
	if ev.ObservedAt != nil {
		observed = ev.ObservedAt.AsTime()
	}
	cluster := ev.Cluster
	if cluster == "" {
		cluster = agent.Cluster
	}
	return &model.Deploy{
		ID: id, AgentID: agent.ID, Cluster: cluster, Namespace: ev.Namespace, Kind: ev.Kind, Name: ev.Name,
		Tier: model.Tier(ev.Tier), Controller: ev.Controller,
		ImagesBefore: imagesFromProto(ev.ImagesBefore), ImagesAfter: imagesFromProto(ev.ImagesAfter),
		TemplateHashBefore: ev.TemplateHashBefore, TemplateHashAfter: ev.TemplateHashAfter,
		RevisionBefore: ev.RevisionBefore, RevisionAfter: ev.RevisionAfter,
		Labels: ev.Labels, Annotations: ev.Annotations, ObservedAt: observed,
	}
}

func imagesFromProto(in []*agentv1.ContainerImage) []model.ContainerImage {
	out := make([]model.ContainerImage, 0, len(in))
	for _, c := range in {
		out = append(out, model.ContainerImage{Container: c.Container, Image: c.Image})
	}
	return out
}

// Now is exposed for the agent package's heartbeat helper so both sides stamp
// the same way.
func Now() *timestamppb.Timestamp { return timestamppb.Now() }
