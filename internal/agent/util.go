package agent

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func containerImages(cs []corev1.Container) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Image)
	}
	return out
}

func deref(p *int32, def int32) int32 {
	if p == nil {
		return def
	}
	return *p
}

func nestedSlice(obj map[string]any, fields ...string) ([]any, bool, error) {
	return unstructured.NestedSlice(obj, fields...)
}

func nestedInt(obj map[string]any, fields ...string) (int64, bool, error) {
	return unstructured.NestedInt64(obj, fields...)
}
