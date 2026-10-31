// Package watcher turns pod-template changes on workloads into DeployEvents,
// whatever applied the change. It is the tier-1 and tier-2 trigger
// (SPEC.md, integration tiers).
package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	agentv1 "github.com/sarkar-ai-taken/hoofprint/internal/agentproto/hoofprint/agent/v1"
	"github.com/sarkar-ai-taken/hoofprint/internal/imageref"
)

// GVRs for the progressive-delivery controllers.
var (
	RolloutGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "rollouts"}
	CanaryGVR  = schema.GroupVersionResource{Group: "flagger.app", Version: "v1beta1", Resource: "canaries"}
)

// Options configure a Watcher.
type Options struct {
	Cluster string
	// WatchRollouts/WatchCanaries enable the dynamic informers; set them from
	// detected capabilities so the agent never watches a CRD that is absent.
	WatchRollouts bool
	WatchCanaries bool
	// Namespaces limits the watch; empty means all namespaces.
	Namespaces []string
	Resync     time.Duration
	// Debounce coalesces template changes to the same workload that land
	// within this window into one event: a user change followed by the
	// controller normalising the spec (Argo Rollouts adds resources: {}),
	// or a multi-step kubectl edit. Default 2s.
	Debounce time.Duration
	Log      *slog.Logger
}

// Watcher runs the informers and emits DeployEvents.
type Watcher struct {
	opts Options
	kube kubernetes.Interface
	dyn  dynamic.Interface
	emit func(*agentv1.DeployEvent)
	log  *slog.Logger

	mu sync.RWMutex
	// rolloutRefs: ns/name of Deployments referenced by a Rollout's workloadRef.
	// Changes to those Deployments are reported through the Rollout instead.
	rolloutRefs map[string]bool
	// flaggerTargets: ns/name of Deployments targeted by a Flagger Canary.
	flaggerTargets map[string]bool

	pmu     sync.Mutex
	pending map[string]*pendingEvent // by kind/ns/name
}

type pendingEvent struct {
	ev    *agentv1.DeployEvent
	timer *time.Timer
}

// New builds a Watcher. dyn may be nil when neither CRD watch is enabled.
func New(kube kubernetes.Interface, dyn dynamic.Interface, opts Options, emit func(*agentv1.DeployEvent)) *Watcher {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Resync == 0 {
		opts.Resync = 10 * time.Minute
	}
	if opts.Debounce == 0 {
		opts.Debounce = 2 * time.Second
	}
	return &Watcher{opts: opts, kube: kube, dyn: dyn, emit: emit, log: opts.Log,
		rolloutRefs: map[string]bool{}, flaggerTargets: map[string]bool{}, pending: map[string]*pendingEvent{}}
}

// queue debounces ev. The first change for a workload opens a window; later
// changes inside it keep the original "before" side and take the newest
// "after" side. When the window closes the merged event is emitted, unless
// the template ended up back where it started.
func (w *Watcher) queue(ev *agentv1.DeployEvent) {
	key := ev.Kind + "/" + ev.Namespace + "/" + ev.Name
	w.pmu.Lock()
	defer w.pmu.Unlock()
	if p, ok := w.pending[key]; ok {
		p.ev.ImagesAfter = ev.ImagesAfter
		p.ev.TemplateHashAfter = ev.TemplateHashAfter
		p.ev.RevisionAfter = ev.RevisionAfter
		p.ev.Labels = ev.Labels
		p.ev.Annotations = ev.Annotations
		p.ev.Tier = ev.Tier
		p.ev.Controller = ev.Controller
		p.ev.ObservedAt = ev.ObservedAt
		p.timer.Reset(w.opts.Debounce)
		w.log.Debug("coalesced template change", "key", key)
		return
	}
	p := &pendingEvent{ev: ev}
	p.timer = time.AfterFunc(w.opts.Debounce, func() { w.flush(key) })
	w.pending[key] = p
}

func (w *Watcher) flush(key string) {
	w.pmu.Lock()
	p, ok := w.pending[key]
	delete(w.pending, key)
	w.pmu.Unlock()
	if !ok {
		return
	}
	if p.ev.TemplateHashBefore == p.ev.TemplateHashAfter {
		w.log.Debug("template change reverted within debounce window; dropping", "key", key)
		return
	}
	w.emit(p.ev)
}

// Run starts the informers and blocks until ctx is done.
func (w *Watcher) Run(ctx context.Context) error {
	ns := metav1.NamespaceAll
	if len(w.opts.Namespaces) == 1 {
		ns = w.opts.Namespaces[0]
	}
	factory := informers.NewSharedInformerFactoryWithOptions(w.kube, w.opts.Resync, informers.WithNamespace(ns))
	var synced []cache.InformerSynced

	dep := factory.Apps().V1().Deployments().Informer()
	_, _ = dep.AddEventHandler(cache.ResourceEventHandlerFuncs{UpdateFunc: func(oldObj, newObj any) {
		o, n := oldObj.(*appsv1.Deployment), newObj.(*appsv1.Deployment)
		w.onWorkload("Deployment", &n.ObjectMeta, &o.Spec.Template, &n.Spec.Template)
	}})
	sts := factory.Apps().V1().StatefulSets().Informer()
	_, _ = sts.AddEventHandler(cache.ResourceEventHandlerFuncs{UpdateFunc: func(oldObj, newObj any) {
		o, n := oldObj.(*appsv1.StatefulSet), newObj.(*appsv1.StatefulSet)
		w.onWorkload("StatefulSet", &n.ObjectMeta, &o.Spec.Template, &n.Spec.Template)
	}})
	ds := factory.Apps().V1().DaemonSets().Informer()
	_, _ = ds.AddEventHandler(cache.ResourceEventHandlerFuncs{UpdateFunc: func(oldObj, newObj any) {
		o, n := oldObj.(*appsv1.DaemonSet), newObj.(*appsv1.DaemonSet)
		w.onWorkload("DaemonSet", &n.ObjectMeta, &o.Spec.Template, &n.Spec.Template)
	}})
	synced = append(synced, dep.HasSynced, sts.HasSynced, ds.HasSynced)
	factory.Start(ctx.Done())

	if w.dyn != nil && (w.opts.WatchRollouts || w.opts.WatchCanaries) {
		dfactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(w.dyn, w.opts.Resync, ns, nil)
		if w.opts.WatchRollouts {
			inf := dfactory.ForResource(RolloutGVR).Informer()
			_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc:    func(obj any) { w.indexRollout(obj.(*unstructured.Unstructured), true) },
				DeleteFunc: func(obj any) { w.indexRollout(asUnstructured(obj), false) },
				UpdateFunc: func(oldObj, newObj any) {
					o, n := oldObj.(*unstructured.Unstructured), newObj.(*unstructured.Unstructured)
					w.indexRollout(n, true)
					w.onRollout(o, n)
				},
			})
			synced = append(synced, inf.HasSynced)
		}
		if w.opts.WatchCanaries {
			inf := dfactory.ForResource(CanaryGVR).Informer()
			_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc:    func(obj any) { w.indexCanary(obj.(*unstructured.Unstructured), true) },
				UpdateFunc: func(_, newObj any) { w.indexCanary(newObj.(*unstructured.Unstructured), true) },
				DeleteFunc: func(obj any) { w.indexCanary(asUnstructured(obj), false) },
			})
			synced = append(synced, inf.HasSynced)
		}
		dfactory.Start(ctx.Done())
	}

	if !cache.WaitForCacheSync(ctx.Done(), synced...) {
		return ctx.Err()
	}
	w.log.Info("workload watcher synced", "rollouts", w.opts.WatchRollouts, "canaries", w.opts.WatchCanaries, "namespace", ns)
	<-ctx.Done()
	return nil
}

func asUnstructured(obj any) *unstructured.Unstructured {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}
	u, _ := obj.(*unstructured.Unstructured)
	return u
}

func (w *Watcher) indexRollout(u *unstructured.Unstructured, present bool) {
	if u == nil {
		return
	}
	name, found, _ := unstructured.NestedString(u.Object, "spec", "workloadRef", "name")
	if !found || name == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	key := u.GetNamespace() + "/" + name
	if present {
		w.rolloutRefs[key] = true
	} else {
		delete(w.rolloutRefs, key)
	}
}

func (w *Watcher) indexCanary(u *unstructured.Unstructured, present bool) {
	if u == nil {
		return
	}
	name, found, _ := unstructured.NestedString(u.Object, "spec", "targetRef", "name")
	if !found || name == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	key := u.GetNamespace() + "/" + name
	if present {
		w.flaggerTargets[key] = true
	} else {
		delete(w.flaggerTargets, key)
	}
}

// onWorkload is the apps/v1 path. It emits only when the pod template changed.
func (w *Watcher) onWorkload(kind string, meta *metav1.ObjectMeta, oldTpl, newTpl *corev1.PodTemplateSpec) {
	ev := Diff(w.opts.Cluster, kind, meta, oldTpl, newTpl)
	if ev == nil {
		return
	}
	key := meta.Namespace + "/" + meta.Name
	w.mu.RLock()
	viaRollout := w.rolloutRefs[key]
	flagger := w.flaggerTargets[key]
	primaryOf := strings.TrimSuffix(meta.Name, "-primary")
	flaggerPrimary := primaryOf != meta.Name && w.flaggerTargets[meta.Namespace+"/"+primaryOf]
	w.mu.RUnlock()

	switch {
	case viaRollout:
		// The Rollout informer reports this change with tier 2 context.
		w.log.Debug("skipping Deployment change managed by Rollout workloadRef", "ns", meta.Namespace, "name", meta.Name)
		return
	case flaggerPrimary:
		// Flagger promoting the canary into <name>-primary is not a new deploy.
		w.log.Debug("skipping Flagger primary promotion", "ns", meta.Namespace, "name", meta.Name)
		return
	case flagger:
		ev.Tier = agentv1.Tier_TIER_PROGRESSIVE
		ev.Controller = "flagger"
	}
	w.queue(ev)
}

// onRollout handles argoproj.io Rollout updates (tier 2).
func (w *Watcher) onRollout(o, n *unstructured.Unstructured) {
	oldTpl, _, _ := unstructured.NestedMap(o.Object, "spec", "template")
	newTpl, _, _ := unstructured.NestedMap(n.Object, "spec", "template")
	oldHash, newHash := hashAny(oldTpl), hashAny(newTpl)
	if oldHash == newHash {
		return
	}
	meta := metav1.ObjectMeta{Namespace: n.GetNamespace(), Name: n.GetName(), Labels: n.GetLabels(), Annotations: n.GetAnnotations()}
	before, after := imagesFromUnstructured(oldTpl), imagesFromUnstructured(newTpl)
	tplAnn, _, _ := unstructured.NestedStringMap(newTpl, "metadata", "annotations")
	tplLbl, _, _ := unstructured.NestedStringMap(newTpl, "metadata", "labels")
	oldTplAnn, _, _ := unstructured.NestedStringMap(oldTpl, "metadata", "annotations")
	oldTplLbl, _, _ := unstructured.NestedStringMap(oldTpl, "metadata", "labels")
	ev := newEvent(w.opts.Cluster, "Rollout", &meta, before, after, oldHash, newHash,
		imageref.Revision(oldTplAnn, oldTplLbl, imageStrings(before)),
		imageref.Revision(tplAnn, tplLbl, imageStrings(after)))
	ev.Tier = agentv1.Tier_TIER_PROGRESSIVE
	ev.Controller = "argo-rollouts"
	w.queue(ev)
}

// Diff returns a tier-1 DeployEvent if the pod template changed, else nil.
// It is exported so the decision logic is testable without informers.
func Diff(cluster, kind string, meta *metav1.ObjectMeta, oldTpl, newTpl *corev1.PodTemplateSpec) *agentv1.DeployEvent {
	oldHash, newHash := TemplateHash(oldTpl), TemplateHash(newTpl)
	if oldHash == newHash {
		return nil
	}
	before, after := images(oldTpl), images(newTpl)
	ev := newEvent(cluster, kind, meta, before, after, oldHash, newHash,
		imageref.Revision(oldTpl.Annotations, oldTpl.Labels, imageStrings(before)),
		imageref.Revision(newTpl.Annotations, newTpl.Labels, imageStrings(after)))
	ev.Tier = agentv1.Tier_TIER_WORKLOAD
	return ev
}

func newEvent(cluster, kind string, meta *metav1.ObjectMeta, before, after []*agentv1.ContainerImage, oldHash, newHash, revBefore, revAfter string) *agentv1.DeployEvent {
	return &agentv1.DeployEvent{
		Id: uuid.NewString(), Cluster: cluster, Namespace: meta.Namespace, Kind: kind, Name: meta.Name,
		ImagesBefore: before, ImagesAfter: after,
		TemplateHashBefore: oldHash, TemplateHashAfter: newHash,
		RevisionBefore: revBefore, RevisionAfter: revAfter,
		Labels: meta.Labels, Annotations: pruneAnnotations(meta.Annotations),
		ObservedAt: timestamppb.Now(),
	}
}

// pruneAnnotations drops the noisy, large annotations controllers leave
// behind so events stay small.
func pruneAnnotations(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range in {
		if k == "kubectl.kubernetes.io/last-applied-configuration" || len(v) > 1024 {
			continue
		}
		out[k] = v
	}
	return out
}

// TemplateHash is a stable digest of the pod template spec. Only the spec is
// hashed: template metadata (labels/annotations) changes are included via the
// metadata labels and annotations since they can carry config, but the
// workload's own status and replica count never are.
func TemplateHash(t *corev1.PodTemplateSpec) string {
	if t == nil {
		return ""
	}
	// Hash spec plus template labels/annotations; a config change expressed
	// as an annotation (checksum/config) is a deploy too.
	return hashAny(map[string]any{"spec": t.Spec, "labels": t.Labels, "annotations": t.Annotations})
}

func hashAny(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func images(t *corev1.PodTemplateSpec) []*agentv1.ContainerImage {
	if t == nil {
		return nil
	}
	out := make([]*agentv1.ContainerImage, 0, len(t.Spec.Containers))
	for _, c := range t.Spec.Containers {
		out = append(out, &agentv1.ContainerImage{Container: c.Name, Image: c.Image})
	}
	return out
}

func imagesFromUnstructured(tpl map[string]any) []*agentv1.ContainerImage {
	containers, _, _ := unstructured.NestedSlice(tpl, "spec", "containers")
	out := make([]*agentv1.ContainerImage, 0, len(containers))
	for _, c := range containers {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		image, _ := m["image"].(string)
		out = append(out, &agentv1.ContainerImage{Container: name, Image: image})
	}
	return out
}

func imageStrings(in []*agentv1.ContainerImage) []string {
	out := make([]string, 0, len(in))
	for _, c := range in {
		out = append(out, c.Image)
	}
	return out
}
