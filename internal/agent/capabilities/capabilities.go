// Package capabilities detects what the agent can do in its cluster, so the
// control plane can route work and classify deploys by tier.
package capabilities

import (
	"sort"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// Well-known capability names advertised at registration.
const (
	Kubernetes         = "kubernetes"
	ArgoRollouts       = "argo-rollouts"
	ArgoCD             = "argo-cd"
	Flagger            = "flagger"
	Tekton             = "tekton"
	PrometheusOperator = "prometheus-operator"
	Prometheus         = "prometheus"
)

// GroupVersions the agent probes, and the resource that proves the
// capability is installed.
var probes = []struct {
	cap      string
	gv       schema.GroupVersion
	resource string
}{
	{ArgoRollouts, schema.GroupVersion{Group: "argoproj.io", Version: "v1alpha1"}, "rollouts"},
	{ArgoCD, schema.GroupVersion{Group: "argoproj.io", Version: "v1alpha1"}, "applications"},
	{Flagger, schema.GroupVersion{Group: "flagger.app", Version: "v1beta1"}, "canaries"},
	{Tekton, schema.GroupVersion{Group: "tekton.dev", Version: "v1"}, "pipelineruns"},
	{PrometheusOperator, schema.GroupVersion{Group: "monitoring.coreos.com", Version: "v1"}, "prometheuses"},
}

// Detect probes the API server for the CRDs each integration installs.
// Extra capabilities (e.g. "prometheus" when a URL is configured) are
// appended. The result is sorted and always includes "kubernetes".
func Detect(disco discovery.DiscoveryInterface, extra ...string) []string {
	caps := map[string]bool{Kubernetes: true}
	for _, p := range probes {
		list, err := disco.ServerResourcesForGroupVersion(p.gv.String())
		if err != nil || list == nil {
			continue
		}
		for _, r := range list.APIResources {
			if r.Name == p.resource {
				caps[p.cap] = true
				break
			}
		}
	}
	for _, e := range extra {
		if e != "" {
			caps[e] = true
		}
	}
	out := make([]string, 0, len(caps))
	for c := range caps {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// Has reports whether caps contains c.
func Has(caps []string, c string) bool {
	for _, x := range caps {
		if x == c {
			return true
		}
	}
	return false
}
