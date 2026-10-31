package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/sarkar-ai-taken/hoofprint/internal/agentproto/hoofprint/agent/v1"
	"github.com/sarkar-ai-taken/hoofprint/internal/model"
	"github.com/sarkar-ai-taken/hoofprint/internal/store/memory"
)

type harness struct {
	st      *memory.Store
	reg     *Registry
	disp    *Dispatcher
	srv     *Server
	addr    string
	deploys chan *model.Deploy
}

func newHarness(t *testing.T, token string) *harness {
	t.Helper()
	st := memory.New()
	log := testLogger()
	reg := NewRegistry(st, log, 50*time.Millisecond, 300*time.Millisecond)
	disp := NewDispatcher(reg)
	h := &harness{st: st, reg: reg, disp: disp, deploys: make(chan *model.Deploy, 8)}
	h.srv = NewServer(st, reg, disp, log, WithAgentToken(token), WithDeployHandler(func(_ context.Context, d *model.Deploy) { h.deploys <- d }))

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := grpc.NewServer()
	h.srv.Register(g)
	go func() { _ = g.Serve(lis) }()
	ctx, cancel := context.WithCancel(context.Background())
	go reg.RunReaper(ctx)
	t.Cleanup(func() { cancel(); g.Stop() })
	h.addr = lis.Addr().String()
	return h
}

// testLogger writes server logs to stderr under -v and discards them
// otherwise. Logging through t.Log is unsafe here: server goroutines outlive
// the test function and t.Log panics once the test has completed.
func testLogger() *slog.Logger {
	var w io.Writer = io.Discard
	if testing.Verbose() {
		w = os.Stderr
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// fakeAgent is a minimal client speaking the protocol by hand.
type fakeAgent struct {
	stream agentv1.AgentService_ConnectClient
	conn   *grpc.ClientConn
}

func dial(t *testing.T, ctx context.Context, addr, token string, reg *agentv1.Register) (*fakeAgent, error) {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	}
	stream, err := agentv1.NewAgentServiceClient(conn).Connect(ctx)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := stream.Send(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_Register{Register: reg}}); err != nil {
		conn.Close()
		return nil, err
	}
	first, err := stream.Recv()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if first.GetRegistered() == nil {
		t.Fatalf("expected Registered, got %v", first)
	}
	return &fakeAgent{stream: stream, conn: conn}, nil
}

func TestRegisterHeartbeatAndWork(t *testing.T) {
	h := newHarness(t, "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	a, err := dial(t, ctx, h.addr, "secret", &agentv1.Register{
		AgentId: "a1", Cluster: "kind", Version: "test", Capabilities: []string{"kubernetes", "argo-rollouts"},
		Tags: map[string]string{"env": "dev"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.conn.Close()

	got, err := h.st.GetAgent(ctx, "a1")
	if err != nil || got.Status != model.AgentOnline || !got.HasCapability("argo-rollouts") {
		t.Fatalf("agent not registered online: %+v %v", got, err)
	}
	if len(h.reg.Online(map[string]string{"env": "dev"}, "argo-rollouts")) != 1 {
		t.Fatal("agent should be online and selectable")
	}
	if len(h.reg.Online(map[string]string{"env": "prod"}, "")) != 0 {
		t.Fatal("selector mismatch should exclude")
	}

	// Agent side: answer work items, send heartbeats.
	go func() {
		for {
			msg, err := a.stream.Recv()
			if err != nil {
				return
			}
			if wi := msg.GetWorkItem(); wi != nil {
				_ = a.stream.Send(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_WorkAck{WorkAck: &agentv1.WorkAck{WorkId: wi.Id}}})
				_ = a.stream.Send(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_WorkEvent{WorkEvent: &agentv1.WorkEvent{
					WorkId: wi.Id, Event: &agentv1.WorkEvent_Log{Log: &agentv1.LogLine{Line: "working on " + wi.Kind}},
				}}})
				var in map[string]string
				_ = json.Unmarshal(wi.Payload, &in)
				out, _ := json.Marshal(map[string]string{"echo": in["say"]})
				_ = a.stream.Send(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_WorkEvent{WorkEvent: &agentv1.WorkEvent{
					WorkId: wi.Id, Event: &agentv1.WorkEvent_Result{Result: &agentv1.WorkResult{Ok: true, Payload: out}},
				}}})
			}
		}
	}()
	go func() {
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := a.stream.Send(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_Heartbeat{Heartbeat: &agentv1.Heartbeat{SentAt: timestamppb.Now()}}}); err != nil {
					return
				}
			}
		}
	}()

	res, err := h.disp.Execute(ctx, WorkRequest{Selector: map[string]string{"env": "dev"}, Kind: "echo", Payload: json.RawMessage(`{"say":"hi"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || string(res.Payload) != `{"echo":"hi"}` || len(res.Logs) != 1 || res.AgentID != "a1" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if _, err := h.disp.Execute(ctx, WorkRequest{AgentID: "nope", Kind: "echo"}); err == nil {
		t.Fatal("unknown agent should error")
	}
	if _, err := h.disp.Execute(ctx, WorkRequest{Selector: map[string]string{"env": "prod"}, Kind: "echo"}); err == nil {
		t.Fatal("selector miss should error")
	}

	// Deploy event is persisted and handed to the handler.
	_ = a.stream.Send(&agentv1.AgentMessage{Msg: &agentv1.AgentMessage_DeployEvent{DeployEvent: &agentv1.DeployEvent{
		Id: "d1", Namespace: "default", Kind: "Deployment", Name: "api", Tier: agentv1.Tier_TIER_WORKLOAD,
		ImagesBefore:   []*agentv1.ContainerImage{{Container: "app", Image: "api:sha-aaaaaaa"}},
		ImagesAfter:    []*agentv1.ContainerImage{{Container: "app", Image: "api:sha-bbbbbbb"}},
		RevisionBefore: "aaaaaaa", RevisionAfter: "bbbbbbb", ObservedAt: timestamppb.Now(),
	}}})
	select {
	case d := <-h.deploys:
		if d.ID != "d1" || d.Cluster != "kind" || d.AgentID != "a1" || d.Tier != model.TierWorkload {
			t.Fatalf("deploy handler got %+v", d)
		}
	case <-ctx.Done():
		t.Fatal("deploy handler not called")
	}
	stored, err := h.st.GetDeploy(ctx, "d1")
	if err != nil || stored.ImagesAfter[0].Image != "api:sha-bbbbbbb" {
		t.Fatalf("deploy not persisted: %+v %v", stored, err)
	}

	// Heartbeats keep it online well past the offline threshold.
	time.Sleep(500 * time.Millisecond)
	got, _ = h.st.GetAgent(ctx, "a1")
	if got.Status != model.AgentOnline {
		t.Fatal("heartbeating agent should stay online")
	}
}

func TestReaperMarksSilentAgentOffline(t *testing.T) {
	h := newHarness(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := dial(t, ctx, h.addr, "", &agentv1.Register{AgentId: "quiet", Cluster: "kind"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.conn.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := h.st.GetAgent(ctx, "quiet")
		if got.Status == model.AgentOffline && h.reg.Count() == 0 {
			// Stream should have been closed by the control plane.
			if _, err := a.stream.Recv(); err == nil {
				t.Fatal("expected stream to be closed")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("silent agent never marked offline")
}

func TestDisconnectMarksOfflineAndFailsInflightWork(t *testing.T) {
	h := newHarness(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := dial(t, ctx, h.addr, "", &agentv1.Register{AgentId: "flaky", Cluster: "kind"})
	if err != nil {
		t.Fatal(err)
	}

	errc := make(chan error, 1)
	go func() {
		_, err := h.disp.Execute(ctx, WorkRequest{AgentID: "flaky", Kind: "slow"})
		errc <- err
	}()
	// Wait for the work item to arrive, then drop the connection.
	if _, err := a.stream.Recv(); err != nil {
		t.Fatal(err)
	}
	a.conn.Close()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("in-flight work should fail on disconnect")
		}
	case <-ctx.Done():
		t.Fatal("Execute did not return after disconnect")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := h.st.GetAgent(ctx, "flaky"); got.Status == model.AgentOffline {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("disconnected agent not marked offline")
}

func TestAuthAndValidation(t *testing.T) {
	h := newHarness(t, "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := dial(t, ctx, h.addr, "wrong", &agentv1.Register{AgentId: "x", Cluster: "c"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated, got %v", err)
	}
	if _, err := dial(t, ctx, h.addr, "secret", &agentv1.Register{AgentId: "", Cluster: "c"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func TestReconnectReplacesSession(t *testing.T) {
	h := newHarness(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a1, err := dial(t, ctx, h.addr, "", &agentv1.Register{AgentId: "same", Cluster: "kind", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	defer a1.conn.Close()
	a2, err := dial(t, ctx, h.addr, "", &agentv1.Register{AgentId: "same", Cluster: "kind", Version: "2"})
	if err != nil {
		t.Fatal(err)
	}
	defer a2.conn.Close()
	if _, err := a1.stream.Recv(); err == nil {
		t.Fatal("old stream should be closed")
	}
	if h.reg.Count() != 1 {
		t.Fatalf("want 1 session, got %d", h.reg.Count())
	}
	got, _ := h.st.GetAgent(ctx, "same")
	if got.Version != "2" || got.Status != model.AgentOnline {
		t.Fatalf("new session should win: %+v", got)
	}
}
