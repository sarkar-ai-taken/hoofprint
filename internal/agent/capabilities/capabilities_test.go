package capabilities

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestDetect(t *testing.T) {
	disco := &fake.FakeDiscovery{Fake: &k8stesting.Fake{}}
	disco.Resources = []*metav1.APIResourceList{
		{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{{Name: "deployments"}}},
		{GroupVersion: "argoproj.io/v1alpha1", APIResources: []metav1.APIResource{{Name: "rollouts"}, {Name: "analysisruns"}}},
		{GroupVersion: "monitoring.coreos.com/v1", APIResources: []metav1.APIResource{{Name: "servicemonitors"}}},
	}
	got := Detect(disco, Prometheus)
	want := []string{ArgoRollouts, Kubernetes, Prometheus}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if Has(got, Flagger) || !Has(got, ArgoRollouts) {
		t.Fatal("Has")
	}
	// prometheus-operator needs the "prometheuses" resource, not just the group.
	if Has(got, PrometheusOperator) {
		t.Fatal("servicemonitors alone should not imply prometheus-operator")
	}
}

func TestDetectBareCluster(t *testing.T) {
	disco := &fake.FakeDiscovery{Fake: &k8stesting.Fake{}}
	got := Detect(disco)
	if len(got) != 1 || got[0] != Kubernetes {
		t.Fatalf("bare cluster: %v", got)
	}
}
