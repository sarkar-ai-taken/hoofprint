// Package storetest is a conformance suite every Store implementation must pass.
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sarkar-ai-taken/hoofprint/internal/model"
	"github.com/sarkar-ai-taken/hoofprint/internal/store"
)

// Run executes the conformance suite against s.
func Run(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("agents", func(t *testing.T) {
		a := &model.Agent{
			ID: "agent-1", Cluster: "kind", Version: "0.0.1",
			Capabilities:  []string{"kubernetes", "argo-rollouts"},
			Tags:          map[string]string{"env": "dev"},
			Status:        model.AgentOnline,
			LastHeartbeat: time.Now().UTC().Truncate(time.Microsecond),
		}
		if err := s.UpsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetAgent(ctx, "agent-1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Cluster != "kind" || len(got.Capabilities) != 2 || got.Tags["env"] != "dev" {
			t.Fatalf("round trip mismatch: %+v", got)
		}
		if got.RegisteredAt.IsZero() {
			t.Fatal("RegisteredAt should be set on first upsert")
		}
		first := got.RegisteredAt

		// Re-register with new version: registration time preserved.
		a.Version = "0.0.2"
		a.RegisteredAt = time.Time{}
		if err := s.UpsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
		got, _ = s.GetAgent(ctx, "agent-1")
		if got.Version != "0.0.2" || !got.RegisteredAt.Equal(first) {
			t.Fatalf("upsert should update version and keep RegisteredAt: %+v", got)
		}

		if err := s.SetAgentStatus(ctx, "agent-1", model.AgentOffline); err != nil {
			t.Fatal(err)
		}
		got, _ = s.GetAgent(ctx, "agent-1")
		if got.Status != model.AgentOffline {
			t.Fatalf("status not updated: %v", got.Status)
		}
		if err := s.TouchAgent(ctx, "agent-1"); err != nil {
			t.Fatal(err)
		}
		got, _ = s.GetAgent(ctx, "agent-1")
		if got.Status != model.AgentOnline || got.LastHeartbeat.Before(a.LastHeartbeat) {
			t.Fatalf("touch should set online and bump heartbeat: %+v", got)
		}

		if _, err := s.GetAgent(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
		if err := s.SetAgentStatus(ctx, "nope", model.AgentOffline); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
		list, err := s.ListAgents(ctx)
		if err != nil || len(list) != 1 {
			t.Fatalf("list: %v %d", err, len(list))
		}
	})

	t.Run("deploys and verdicts", func(t *testing.T) {
		if err := s.UpsertAgent(ctx, &model.Agent{ID: "agent-d", Cluster: "kind"}); err != nil {
			t.Fatal(err)
		}
		base := time.Now().UTC().Truncate(time.Microsecond)
		for i, name := range []string{"api", "worker", "api"} {
			d := &model.Deploy{
				ID: "dep-" + string(rune('a'+i)), AgentID: "agent-d", Cluster: "kind",
				Namespace: "default", Kind: "Deployment", Name: name, Tier: model.TierWorkload,
				ImagesBefore:       []model.ContainerImage{{Container: "app", Image: "app:sha-aaaaaaa"}},
				ImagesAfter:        []model.ContainerImage{{Container: "app", Image: "app:sha-bbbbbbb"}},
				TemplateHashBefore: "h1", TemplateHashAfter: "h2",
				RevisionBefore: "aaaaaaa", RevisionAfter: "bbbbbbb",
				Labels:     map[string]string{"app": name},
				ObservedAt: base.Add(time.Duration(i) * time.Second),
			}
			if i == 1 {
				d.Namespace = "jobs"
			}
			if err := s.InsertDeploy(ctx, d); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.GetDeploy(ctx, "dep-a")
		if err != nil {
			t.Fatal(err)
		}
		if got.ImagesAfter[0].Image != "app:sha-bbbbbbb" || got.Labels["app"] != "api" || got.Tier != model.TierWorkload {
			t.Fatalf("deploy round trip mismatch: %+v", got)
		}
		all, err := s.ListDeploys(ctx, model.DeployFilter{})
		if err != nil || len(all) != 3 {
			t.Fatalf("list all: %v %d", err, len(all))
		}
		if all[0].ID != "dep-c" {
			t.Fatalf("want newest first, got %s", all[0].ID)
		}
		ns, _ := s.ListDeploys(ctx, model.DeployFilter{Namespace: "jobs"})
		if len(ns) != 1 || ns[0].ID != "dep-b" {
			t.Fatalf("namespace filter: %+v", ns)
		}
		lim, _ := s.ListDeploys(ctx, model.DeployFilter{Limit: 2})
		if len(lim) != 2 {
			t.Fatalf("limit: %d", len(lim))
		}

		v := &model.Verdict{
			ID: "ver-1", DeployID: "dep-a", Judge: "stats", Outcome: model.OutcomeFail,
			Confidence: 0.93, Rationale: "p99 latency up 4x vs baseline",
		}
		ev := []*model.Evidence{
			{ID: "ev-1", Kind: "metric", Title: "p99 latency", Payload: []byte(`{"baseline":120,"canary":480}`)},
			{ID: "ev-2", Kind: "event", Title: "BackOff", Payload: []byte(`{"count":3}`)},
		}
		if err := s.InsertVerdict(ctx, v, ev); err != nil {
			t.Fatal(err)
		}
		gv, gev, err := s.GetVerdict(ctx, "ver-1")
		if err != nil {
			t.Fatal(err)
		}
		if gv.Outcome != model.OutcomeFail || gv.Confidence != 0.93 || len(gev) != 2 {
			t.Fatalf("verdict round trip: %+v %d", gv, len(gev))
		}
		if gev[0].VerdictID != "ver-1" || string(gev[0].Payload) != `{"baseline":120,"canary":480}` {
			t.Fatalf("evidence round trip: %+v", gev[0])
		}
		vs, err := s.ListVerdicts(ctx, "dep-a")
		if err != nil || len(vs) != 1 {
			t.Fatalf("list verdicts: %v %d", err, len(vs))
		}
		none, _ := s.ListVerdicts(ctx, "dep-b")
		if len(none) != 0 {
			t.Fatalf("expected no verdicts for dep-b")
		}
		if err := s.InsertVerdict(ctx, &model.Verdict{ID: "ver-x", DeployID: "missing"}, nil); err == nil {
			t.Fatal("verdict for unknown deploy should fail")
		}
	})
}
