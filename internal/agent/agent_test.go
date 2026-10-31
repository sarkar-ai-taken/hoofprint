package agent

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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	agentv1 "github.com/sarkar-ai-taken/hoofprint/internal/agentproto/hoofprint/agent/v1"
	"github.com/sarkar-ai-taken/hoofprint/internal/controlplane"
	"github.com/sarkar-ai-taken/hoofprint/internal/model"
	"github.com/sarkar-ai-taken/hoofprint/internal/store/memory"
)

// TestAgentAgainstControlPlane runs the real agent runtime against the real
// control plane server over loopback, with a fake kube client behind it.
func TestAgentAgainstControlPlane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log := testLogger()

	st := memory.New()
	reg := controlplane.NewRegistry(st, log, 100*time.Millisecond, time.Second)
	disp := controlplane.NewDispatcher(reg)
	deploys := make(chan *model.Deploy, 4)
	srv := controlplane.NewServer(st, reg, disp, log, controlplane.WithAgentToken("tok"),
		controlplane.WithDeployHandler(func(_ context.Context, d *model.Deploy) { deploys <- d }))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := grpc.NewServer()
	srv.Register(g)
	go func() { _ = g.Serve(lis) }()
	defer g.Stop()

	replicas := int32(2)
	kube := k8sfake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "api"},
		Spec: appsv1.DeploymentSpec{Replicas: &replicas, Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "api:sha-abc1234"}}}}},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 2},
	})
	a := New(Config{ControlPlane: lis.Addr().String(), Token: "tok", Cluster: "kind", Version: "test",
		Capabilities: []string{"kubernetes"}, Tags: map[string]string{"env": "dev"}, Insecure: true, Log: log})
	a.RegisterKubernetesHandlers(kube, nil, []string{"kubernetes"})
	go func() { _ = a.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for !a.Connected() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !a.Connected() {
		t.Fatal("agent never connected")
	}
	got, err := st.GetAgent(ctx, "agent-kind")
	if err != nil || got.Status != model.AgentOnline || got.Tags["env"] != "dev" {
		t.Fatalf("registration: %+v %v", got, err)
	}

	res, err := disp.Execute(ctx, controlplane.WorkRequest{AgentID: "agent-kind", Kind: KindWorkloadsList, Payload: json.RawMessage(`{"namespace":"default"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Workloads []Workload }
	if err := json.Unmarshal(res.Payload, &out); err != nil || !res.OK {
		t.Fatalf("result: %+v %v", res, err)
	}
	if len(out.Workloads) != 1 || out.Workloads[0].Name != "api" || out.Workloads[0].Revision != "abc1234" || out.Workloads[0].Ready != 2 {
		t.Fatalf("workloads: %+v", out.Workloads)
	}
	if len(res.Logs) == 0 {
		t.Fatal("expected streamed log line")
	}

	res, err = disp.Execute(ctx, controlplane.WorkRequest{AgentID: "agent-kind", Kind: "nope"})
	if err != nil || res.OK || res.Error == "" {
		t.Fatalf("unknown kind should return a failed result: %+v %v", res, err)
	}

	a.EmitDeploy(&agentv1.DeployEvent{Id: "d1", Namespace: "default", Kind: "Deployment", Name: "api", Tier: agentv1.Tier_TIER_WORKLOAD})
	select {
	case d := <-deploys:
		if d.ID != "d1" || d.Cluster != "kind" {
			t.Fatalf("deploy: %+v", d)
		}
	case <-ctx.Done():
		t.Fatal("deploy event not delivered")
	}
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
