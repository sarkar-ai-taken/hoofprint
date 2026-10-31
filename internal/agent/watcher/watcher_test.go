package watcher

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	agentv1 "github.com/sarkar-ai-taken/hoofprint/internal/agentproto/hoofprint/agent/v1"
)

func deployment(name, image string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name, Labels: map[string]string{"app": name}},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: image}}},
			},
		},
	}
}

func TestDiff(t *testing.T) {
	a := deployment("api", "ghcr.io/acme/api:sha-aaaaaaa", 1)
	b := deployment("api", "ghcr.io/acme/api:sha-bbbbbbb", 1)
	ev := Diff("kind", "Deployment", &b.ObjectMeta, &a.Spec.Template, &b.Spec.Template)
	if ev == nil {
		t.Fatal("image change must produce an event")
	}
	if ev.Tier != agentv1.Tier_TIER_WORKLOAD || ev.Kind != "Deployment" || ev.Namespace != "default" || ev.Name != "api" {
		t.Fatalf("event: %+v", ev)
	}
	if ev.RevisionBefore != "aaaaaaa" || ev.RevisionAfter != "bbbbbbb" {
		t.Fatalf("revisions: %q %q", ev.RevisionBefore, ev.RevisionAfter)
	}
	if ev.ImagesAfter[0].Image != "ghcr.io/acme/api:sha-bbbbbbb" || ev.TemplateHashBefore == ev.TemplateHashAfter {
		t.Fatalf("images/hash: %+v", ev)
	}

	// Scaling is not a deploy.
	c := deployment("api", "ghcr.io/acme/api:sha-aaaaaaa", 5)
	if Diff("kind", "Deployment", &c.ObjectMeta, &a.Spec.Template, &c.Spec.Template) != nil {
		t.Fatal("replica change must not produce an event")
	}

	// An env var change is a deploy, even with the same image.
	d := deployment("api", "ghcr.io/acme/api:sha-aaaaaaa", 1)
	d.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "FEATURE_X", Value: "on"}}
	if Diff("kind", "Deployment", &d.ObjectMeta, &a.Spec.Template, &d.Spec.Template) == nil {
		t.Fatal("env change must produce an event")
	}

	// A config checksum annotation on the template is a deploy.
	e := deployment("api", "ghcr.io/acme/api:sha-aaaaaaa", 1)
	e.Spec.Template.Annotations = map[string]string{"checksum/config": "abc"}
	if Diff("kind", "Deployment", &e.ObjectMeta, &a.Spec.Template, &e.Spec.Template) == nil {
		t.Fatal("template annotation change must produce an event")
	}

	// Explicit revision annotation on the template wins over the tag.
	f := deployment("api", "ghcr.io/acme/api:v2", 1)
	f.Spec.Template.Annotations = map[string]string{"org.opencontainers.image.revision": "fedcba9"}
	ev = Diff("kind", "Deployment", &f.ObjectMeta, &a.Spec.Template, &f.Spec.Template)
	if ev.RevisionAfter != "fedcba9" {
		t.Fatalf("annotation revision: %q", ev.RevisionAfter)
	}
}

func TestPruneAnnotations(t *testing.T) {
	in := map[string]string{"kubectl.kubernetes.io/last-applied-configuration": "{...}", "keep": "me", "big": string(make([]byte, 2000))}
	out := pruneAnnotations(in)
	if len(out) != 1 || out["keep"] != "me" {
		t.Fatalf("prune: %v", out)
	}
}

func rolloutObj(ns, name, workloadRef, image string) *unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Rollout",
		"metadata": map[string]any{"namespace": ns, "name": name},
		"spec":     map[string]any{},
	}
	spec := obj["spec"].(map[string]any)
	if workloadRef != "" {
		spec["workloadRef"] = map[string]any{"kind": "Deployment", "name": workloadRef}
	}
	if image != "" {
		spec["template"] = map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "app", "image": image}}}}
	}
	return &unstructured.Unstructured{Object: obj}
}

func canaryObj(ns, name, target string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "flagger.app/v1beta1", "kind": "Canary",
		"metadata": map[string]any{"namespace": ns, "name": name},
		"spec":     map[string]any{"targetRef": map[string]any{"kind": "Deployment", "name": target}},
	}}
}

func waitEvent(t *testing.T, ch <-chan *agentv1.DeployEvent, d time.Duration) *agentv1.DeployEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(d):
		return nil
	}
}

func TestWatcherWithFakeClients(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	kube := k8sfake.NewSimpleClientset(
		deployment("api", "api:sha-a111111", 1),
		deployment("ref", "ref:sha-a111111", 1),
		deployment("shop", "shop:sha-a111111", 1),
		deployment("shop-primary", "shop:sha-a111111", 1),
	)
	scheme := runtime.NewScheme()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		RolloutGVR: "RolloutList", CanaryGVR: "CanaryList",
	}, rolloutObj("default", "ref-rollout", "ref", ""), rolloutObj("default", "pure", "", "pure:sha-a111111"), canaryObj("default", "shop", "shop"))

	events := make(chan *agentv1.DeployEvent, 16)
	w := New(kube, dyn, Options{Cluster: "kind", WatchRollouts: true, WatchCanaries: true, Resync: time.Hour, Debounce: 50 * time.Millisecond}, func(ev *agentv1.DeployEvent) { events <- ev })
	go func() { _ = w.Run(ctx) }()

	// Give informers time to sync (fake clients are fast; indexes must be populated).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.RLock()
		ok := w.rolloutRefs["default/ref"] && w.flaggerTargets["default/shop"]
		w.mu.RUnlock()
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	update := func(d *appsv1.Deployment) {
		if _, err := kube.AppsV1().Deployments("default").Update(ctx, d, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Plain deployment image change -> tier 1 event.
	update(deployment("api", "api:sha-b222222", 1))
	ev := waitEvent(t, events, 5*time.Second)
	if ev == nil || ev.Name != "api" || ev.Tier != agentv1.Tier_TIER_WORKLOAD || ev.RevisionAfter != "b222222" {
		t.Fatalf("plain deployment: %+v", ev)
	}

	// 2. Scale only -> nothing.
	update(deployment("api", "api:sha-b222222", 3))
	if ev := waitEvent(t, events, 300*time.Millisecond); ev != nil {
		t.Fatalf("scale produced event: %+v", ev)
	}

	// 3. Deployment referenced by a Rollout workloadRef -> suppressed.
	update(deployment("ref", "ref:sha-b222222", 1))
	if ev := waitEvent(t, events, 300*time.Millisecond); ev != nil {
		t.Fatalf("workloadRef deployment produced event: %+v", ev)
	}

	// 4. Flagger target -> tier 2 with controller flagger.
	update(deployment("shop", "shop:sha-b222222", 1))
	ev = waitEvent(t, events, 5*time.Second)
	if ev == nil || ev.Tier != agentv1.Tier_TIER_PROGRESSIVE || ev.Controller != "flagger" || ev.Name != "shop" {
		t.Fatalf("flagger target: %+v", ev)
	}

	// 5. Flagger's primary promotion -> suppressed.
	update(deployment("shop-primary", "shop:sha-b222222", 1))
	if ev := waitEvent(t, events, 300*time.Millisecond); ev != nil {
		t.Fatalf("primary promotion produced event: %+v", ev)
	}

	// 6. Rollout template change -> tier 2 argo-rollouts.
	if _, err := dyn.Resource(RolloutGVR).Namespace("default").Update(ctx, rolloutObj("default", "pure", "", "pure:sha-c333333"), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	ev = waitEvent(t, events, 5*time.Second)
	if ev == nil || ev.Kind != "Rollout" || ev.Controller != "argo-rollouts" || ev.RevisionAfter != "c333333" || ev.Tier != agentv1.Tier_TIER_PROGRESSIVE {
		t.Fatalf("rollout: %+v", ev)
	}
}

func TestDebounceCoalescesRapidChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	kube := k8sfake.NewSimpleClientset(deployment("api", "api:sha-a111111", 1))
	events := make(chan *agentv1.DeployEvent, 16)
	w := New(kube, nil, Options{Cluster: "kind", Resync: time.Hour, Debounce: 300 * time.Millisecond}, func(ev *agentv1.DeployEvent) { events <- ev })
	go func() { _ = w.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)

	update := func(d *appsv1.Deployment) {
		if _, err := kube.AppsV1().Deployments("default").Update(ctx, d, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	// User change, then a "controller" normalisation 20ms later.
	update(deployment("api", "api:sha-b222222", 1))
	time.Sleep(20 * time.Millisecond)
	d := deployment("api", "api:sha-b222222", 1)
	d.Spec.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{}
	d.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "X", Value: "1"}}
	update(d)

	ev := waitEvent(t, events, 2*time.Second)
	if ev == nil {
		t.Fatal("expected one coalesced event")
	}
	if ev.RevisionBefore != "a111111" || ev.RevisionAfter != "b222222" {
		t.Fatalf("coalesced before/after: %q -> %q", ev.RevisionBefore, ev.RevisionAfter)
	}
	if ev.TemplateHashAfter != TemplateHash(&d.Spec.Template) {
		t.Fatal("after-side should come from the latest change")
	}
	if extra := waitEvent(t, events, 500*time.Millisecond); extra != nil {
		t.Fatalf("second event leaked through debounce: %+v", extra)
	}

	// A change reverted inside the window is not a deploy.
	update(deployment("api", "api:sha-c333333", 1))
	time.Sleep(20 * time.Millisecond)
	update(d)
	if ev := waitEvent(t, events, 800*time.Millisecond); ev != nil {
		t.Fatalf("reverted change should be dropped: %+v", ev)
	}
}
