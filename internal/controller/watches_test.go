package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllertest"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func TestDriftPredicate(t *testing.T) {
	type metadata = *metav1.PartialObjectMetadata
	object := func(resourceVersion string) metadata {
		return &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns", Name: "settings", ResourceVersion: resourceVersion,
		}}
	}

	tests := []struct {
		name string
		call func(predicate.TypedPredicate[metadata]) bool
		want bool
	}{
		{
			name: "create (the controller created the object, or the watch started)",
			call: func(p predicate.TypedPredicate[metadata]) bool {
				return p.Create(event.TypedCreateEvent[metadata]{Object: object("1")})
			},
			want: false,
		},
		{
			name: "update that changed the object",
			call: func(p predicate.TypedPredicate[metadata]) bool {
				return p.Update(event.TypedUpdateEvent[metadata]{ObjectOld: object("1"), ObjectNew: object("2")})
			},
			want: true,
		},
		{
			name: "resync (same resourceVersion)",
			call: func(p predicate.TypedPredicate[metadata]) bool {
				return p.Update(event.TypedUpdateEvent[metadata]{ObjectOld: object("1"), ObjectNew: object("1")})
			},
			want: false,
		},
		{
			name: "delete (also sent when the controller label is removed)",
			call: func(p predicate.TypedPredicate[metadata]) bool {
				return p.Delete(event.TypedDeleteEvent[metadata]{Object: object("1")})
			},
			want: true,
		},
		{
			name: "generic",
			call: func(p predicate.TypedPredicate[metadata]) bool {
				return p.Generic(event.TypedGenericEvent[metadata]{Object: object("1")})
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.call(driftPredicate()); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// runningController records the sources passed to Watch and starts them, as a running controller
// does.
type runningController struct {
	controller.Controller // only Watch is called

	ctx     context.Context
	sources []source.Source
}

func (c *runningController) Watch(src source.Source) error {
	c.sources = append(c.sources, src)
	return src.Start(c.ctx, nil)
}

// fakeCache gives each kind its own fake informer. An informer has not listed anything until the
// test calls its Synced method. Like the real cache, WaitForCacheSync waits for every informer.
type fakeCache struct {
	cache.Cache // only GetInformer and WaitForCacheSync are called

	mu        sync.Mutex
	informers map[schema.GroupVersionKind]*controllertest.FakeInformer
}

func (c *fakeCache) informer(gvk schema.GroupVersionKind) *controllertest.FakeInformer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.informers == nil {
		c.informers = map[schema.GroupVersionKind]*controllertest.FakeInformer{}
	}
	if c.informers[gvk] == nil {
		c.informers[gvk] = controllertest.NewFakeInformer()
	}
	return c.informers[gvk]
}

func (c *fakeCache) GetInformer(_ context.Context, obj client.Object, _ ...cache.InformerGetOption) (cache.Informer, error) {
	return c.informer(obj.GetObjectKind().GroupVersionKind()), nil
}

func (c *fakeCache) WaitForCacheSync(ctx context.Context) bool {
	c.mu.Lock()
	synced := make([]toolscache.InformerSynced, 0, len(c.informers))
	for _, informer := range c.informers {
		synced = append(synced, informer.HasSynced)
	}
	c.mu.Unlock()
	return toolscache.WaitForCacheSync(ctx.Done(), synced...)
}

// syncAll lets every informer finish listing, so that no goroutine of the test keeps waiting.
func (c *fakeCache) syncAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, informer := range c.informers {
		if !informer.HasSynced() {
			informer.Synced()
		}
	}
}

func newTestRegistry(t *testing.T) (*watchRegistry, *runningController, *fakeCache) {
	running := &runningController{ctx: t.Context()}
	informers := &fakeCache{}
	t.Cleanup(informers.syncAll)
	return &watchRegistry{ctrl: running, cache: informers, watched: map[schema.GroupKind]watchState{}}, running, informers
}

// pollReady calls ensure until it reports the watch of the kind as ready.
func pollReady(t *testing.T, w *watchRegistry, gvk schema.GroupVersionKind) {
	t.Helper()
	err := wait.PollUntilContextTimeout(t.Context(), 10*time.Millisecond, 10*time.Second, true,
		func(ctx context.Context) (bool, error) { return w.ensure(ctx, gvk) })
	if err != nil {
		t.Fatalf("ensure(%s) did not report the watch as ready: %v", gvk, err)
	}
}

func (w *watchRegistry) state(gk schema.GroupKind) watchState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.watched[gk]
}

var (
	widgetV1    = schema.GroupVersionKind{Group: "widget.example.com", Version: "v1", Kind: "Widget"}
	configMapV1 = corev1.SchemeGroupVersion.WithKind("ConfigMap")
)

func TestWatchRegistryOncePerGroupKind(t *testing.T) {
	w, running, _ := newTestRegistry(t)
	steps := []struct {
		name    string
		gvk     schema.GroupVersionKind
		watches int
	}{
		{"first kind", widgetV1, 1},
		{"the same kind again", widgetV1, 1},
		{"another version of the same group and kind", widgetV1.GroupKind().WithVersion("v2"), 1},
		{"another kind", configMapV1, 2},
	}
	for _, step := range steps {
		if _, err := w.ensure(t.Context(), step.gvk); err != nil {
			t.Fatalf("%s: ensure(%s) failed: %v", step.name, step.gvk, err)
		}
		if got := len(running.sources); got != step.watches {
			t.Errorf("%s: ensure(%s) left %d watches, want %d", step.name, step.gvk, got, step.watches)
		}
	}
}

func TestWatchRegistryReadyPerKind(t *testing.T) {
	w, _, informers := newTestRegistry(t)

	// The Widget informer never lists its objects, like the informer of a kind that cannot be
	// listed. It must not keep the ConfigMap watch from becoming ready.
	for _, gvk := range []schema.GroupVersionKind{widgetV1, configMapV1, configMapV1} {
		if ready, err := w.ensure(t.Context(), gvk); err != nil || ready {
			t.Fatalf("ensure(%s) = %v, %v before its informer listed the objects, want false, nil", gvk, ready, err)
		}
	}

	informers.informer(configMapV1).Synced()
	pollReady(t, w, configMapV1)
	if got := w.state(configMapV1.GroupKind()); got != watchSynced {
		t.Errorf("ConfigMap watch state = %v, want %v", got, watchSynced)
	}
	if ready, _ := w.ensure(t.Context(), widgetV1.GroupKind().WithVersion("v2")); ready {
		t.Error("ensure(Widget) = true, want false: its informer has not listed the objects")
	}
}

func TestWatchRegistryGracePeriod(t *testing.T) {
	w, _, _ := newTestRegistry(t)
	w.gracePeriod = 50 * time.Millisecond

	if ready, err := w.ensure(t.Context(), widgetV1); err != nil || ready {
		t.Fatalf("ensure() = %v, %v right after adding the watch, want false, nil", ready, err)
	}
	pollReady(t, w, widgetV1)
	if got := w.state(widgetV1.GroupKind()); got != watchTimedOut {
		t.Errorf("Widget watch state = %v, want %v", got, watchTimedOut)
	}
}
