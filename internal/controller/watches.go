package controller

import (
	"cmp"
	"context"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
)

// newManagedCache returns the cache that serves the drift watches. It holds only the metadata of
// objects with the controller label, without managedFields, so it stays small. Nothing reads from
// it, so the manager's cache is not affected.
func newManagedCache(mgr ctrl.Manager) (cache.Cache, error) {
	req, err := labels.NewRequirement(v1alpha1.ManagedByClassLabel, selection.Exists, nil)
	if err != nil {
		return nil, err
	}
	return cache.New(mgr.GetConfig(), cache.Options{
		HTTPClient: mgr.GetHTTPClient(), Scheme: mgr.GetScheme(), Mapper: mgr.GetRESTMapper(),
		DefaultLabelSelector: labels.NewSelector().Add(*req),
		DefaultTransform:     cache.TransformStripManagedFields(),
	})
}

// defaultWatchGracePeriod is how long a new drift watch may take to list the objects of its kind.
const defaultWatchGracePeriod = 30 * time.Second

// watchState tells whether a drift watch reports every change to the objects of its kind.
type watchState int

const (
	watchStarting watchState = iota // has not listed the existing objects yet, so it misses changes
	watchSynced                     // has listed the existing objects and reports every later change
	watchTimedOut                   // did not list the objects within the grace period
)

// watchRegistry adds one drift watch per group and kind, the first time a class uses the kind.
// Watches are never removed.
type watchRegistry struct {
	mu          sync.Mutex
	ctrl        controller.Controller
	cache       cache.Cache
	gracePeriod time.Duration // zero means defaultWatchGracePeriod
	watched     map[schema.GroupKind]watchState
}

// ensure makes sure that changes to the controller's objects of this kind requeue their
// namespace, and reports whether the watch is ready: it has listed the existing objects, or the
// grace period is over. The version does not matter: a watch on one version reports the objects
// of every version.
func (w *watchRegistry) ensure(ctx context.Context, gvk schema.GroupVersionKind) (ready bool, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	gk := gvk.GroupKind()
	// A second watch for the same kind would add a second event handler.
	if state, ok := w.watched[gk]; ok {
		return state != watchStarting, nil
	}
	obj := &metav1.PartialObjectMetadata{}
	obj.SetGroupVersionKind(gvk)
	// The informer lists the objects in the background. The event handler is added to it right
	// away, before the run reads any object of this kind, so once the informer has listed the
	// objects, the handler sees every later change.
	informer, err := w.cache.GetInformer(ctx, obj, cache.BlockUntilSynced(false))
	if err != nil {
		return false, fmt.Errorf("watch %s: %w", gk, err)
	}
	src := &source.TypedInformer[*metav1.PartialObjectMetadata, reconcile.Request]{
		Informer:   informer,
		Handler:    handler.TypedEnqueueRequestsFromMapFunc(namespaceOf),
		Predicates: []predicate.TypedPredicate[*metav1.PartialObjectMetadata]{driftPredicate()},
	}
	if err := w.ctrl.Watch(src); err != nil {
		return false, fmt.Errorf("watch %s: %w", gk, err)
	}
	w.watched[gk] = watchStarting

	log, grace := logf.FromContext(ctx), cmp.Or(w.gracePeriod, defaultWatchGracePeriod)
	go func() {
		state := waitForList(informer, grace)
		if state == watchTimedOut {
			log.Info("Drift watch did not list its objects within the grace period", "kind", gk.String(), "gracePeriod", grace.String())
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		w.watched[gk] = state
	}()
	return false, nil
}

// waitForList waits until the informer has listed the existing objects, for at most the grace
// period. Only this informer counts, so a kind that cannot be listed does not hold back the
// others. Such a watch (a create-only kind, a missing CRD, an aggregated API that is down) would
// never be ready; after the grace period it counts as ready anyway, which ends the runs that
// Reconcile repeats while it waits.
func waitForList(informer cache.Informer, grace time.Duration) watchState {
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if toolscache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return watchSynced
	}
	return watchTimedOut
}

// driftPredicate passes the events that can mean drift: an update that changed the object, and a
// deletion. An object that loses the controller label leaves the cache, so that also arrives as a
// deletion. Creations do not pass: the controller created the object itself, and every existing
// object arrives as a creation when a watch starts. Resyncs (same resourceVersion) do not pass
// either; the Namespace resync already requeues every namespace that uses a class.
func driftPredicate() predicate.TypedPredicate[*metav1.PartialObjectMetadata] {
	return predicate.TypedFuncs[*metav1.PartialObjectMetadata]{
		CreateFunc: func(event.TypedCreateEvent[*metav1.PartialObjectMetadata]) bool {
			return false
		},
		UpdateFunc: func(e event.TypedUpdateEvent[*metav1.PartialObjectMetadata]) bool {
			return e.ObjectOld.GetResourceVersion() != e.ObjectNew.GetResourceVersion()
		},
		DeleteFunc: func(event.TypedDeleteEvent[*metav1.PartialObjectMetadata]) bool {
			return true
		},
		GenericFunc: func(event.TypedGenericEvent[*metav1.PartialObjectMetadata]) bool {
			return false
		},
	}
}

func namespaceOf(_ context.Context, o *metav1.PartialObjectMetadata) []reconcile.Request {
	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{Name: o.GetNamespace()},
		},
	}
}
