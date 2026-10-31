package agent

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/sarkar-ai-taken/hoofprint/internal/agent/capabilities"
	"github.com/sarkar-ai-taken/hoofprint/internal/agent/watcher"
	"github.com/sarkar-ai-taken/hoofprint/internal/imageref"
)

// Work kinds the agent understands in M0.
const (
	KindPing          = "ping"
	KindWorkloadsList = "workloads.list"
)

// Workload is one row of a workloads.list result.
type Workload struct {
	Namespace string            `json:"namespace"`
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	Replicas  int32             `json:"replicas"`
	Ready     int32             `json:"ready"`
	Images    []string          `json:"images"`
	Revision  string            `json:"revision,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// WorkloadsListInput is the payload of workloads.list.
type WorkloadsListInput struct {
	Namespace string `json:"namespace,omitempty"`
}

// RegisterKubernetesHandlers wires the built-in handlers.
func (a *Agent) RegisterKubernetesHandlers(kube kubernetes.Interface, dyn dynamic.Interface, caps []string) {
	a.Handle(KindPing, func(_ context.Context, payload json.RawMessage, _ func(string)) (json.RawMessage, error) {
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
		return payload, nil
	})
	a.Handle(KindWorkloadsList, func(ctx context.Context, payload json.RawMessage, logf func(string)) (json.RawMessage, error) {
		var in WorkloadsListInput
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &in); err != nil {
				return nil, fmt.Errorf("bad payload: %w", err)
			}
		}
		out, err := listWorkloads(ctx, kube, dyn, caps, in.Namespace, logf)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"workloads": out})
	})
}

func listWorkloads(ctx context.Context, kube kubernetes.Interface, dyn dynamic.Interface, caps []string, ns string, logf func(string)) ([]Workload, error) {
	var out []Workload
	deps, err := kube.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	for _, d := range deps.Items {
		imgs := containerImages(d.Spec.Template.Spec.Containers)
		out = append(out, Workload{Namespace: d.Namespace, Kind: "Deployment", Name: d.Name,
			Replicas: deref(d.Spec.Replicas, 1), Ready: d.Status.ReadyReplicas, Images: imgs,
			Revision: imageref.Revision(d.Spec.Template.Annotations, d.Spec.Template.Labels, imgs), Labels: d.Labels})
	}
	sts, err := kube.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list statefulsets: %w", err)
	}
	for _, s := range sts.Items {
		imgs := containerImages(s.Spec.Template.Spec.Containers)
		out = append(out, Workload{Namespace: s.Namespace, Kind: "StatefulSet", Name: s.Name,
			Replicas: deref(s.Spec.Replicas, 1), Ready: s.Status.ReadyReplicas, Images: imgs,
			Revision: imageref.Revision(s.Spec.Template.Annotations, s.Spec.Template.Labels, imgs), Labels: s.Labels})
	}
	dss, err := kube.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list daemonsets: %w", err)
	}
	for _, d := range dss.Items {
		imgs := containerImages(d.Spec.Template.Spec.Containers)
		out = append(out, Workload{Namespace: d.Namespace, Kind: "DaemonSet", Name: d.Name,
			Replicas: d.Status.DesiredNumberScheduled, Ready: d.Status.NumberReady, Images: imgs,
			Revision: imageref.Revision(d.Spec.Template.Annotations, d.Spec.Template.Labels, imgs), Labels: d.Labels})
	}
	if dyn != nil && capabilities.Has(caps, capabilities.ArgoRollouts) {
		ros, err := dyn.Resource(watcher.RolloutGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			logf("list rollouts: " + err.Error())
		} else {
			for _, r := range ros.Items {
				var imgs []string
				containers, _, _ := nestedSlice(r.Object, "spec", "template", "spec", "containers")
				for _, c := range containers {
					if m, ok := c.(map[string]any); ok {
						if img, ok := m["image"].(string); ok {
							imgs = append(imgs, img)
						}
					}
				}
				replicas, _, _ := nestedInt(r.Object, "spec", "replicas")
				ready, _, _ := nestedInt(r.Object, "status", "readyReplicas")
				out = append(out, Workload{Namespace: r.GetNamespace(), Kind: "Rollout", Name: r.GetName(),
					Replicas: int32(replicas), Ready: int32(ready), Images: imgs,
					Revision: imageref.Revision(nil, nil, imgs), Labels: r.GetLabels()})
			}
		}
	}
	logf(fmt.Sprintf("listed %d workloads", len(out)))
	if out == nil {
		out = []Workload{}
	}
	return out, nil
}
