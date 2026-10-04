package controller

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllertest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
)

var (
	ctx         context.Context
	cancel      context.CancelFunc
	testEnv     *envtest.Environment
	cfg         *rest.Config
	k8sClient   client.Client
	stopManager func()
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(30 * time.Second)
	SetDefaultEventuallyPollingInterval(250 * time.Millisecond)
	SetDefaultConsistentlyDuration(3 * time.Second)
	SetDefaultConsistentlyPollingInterval(250 * time.Millisecond)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	Expect(v1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		UseExistingCluster:    new(false),
	}

	// Retrieve the first found binary directory to allow running tests from IDEs
	if getFirstFoundEnvTestBinaryDir() != "" {
		testEnv.BinaryAssetsDirectory = getFirstFoundEnvTestBinaryDir()
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	By("starting the controller")
	stopManager = startManager()
})

var _ = AfterSuite(func() {
	if stopManager != nil {
		By("stopping the controller")
		stopManager()
	}
	By("tearing down the test environment")
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})

// startManager starts a manager that runs the reconciler, like cmd/main.go does. The returned
// function stops it and waits until it has stopped.
func startManager() (stop func()) {
	GinkgoHelper()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:     scheme.Scheme,
		Metrics:    metricsserver.Options{BindAddress: "0"},
		Cache:      cache.Options{ReaderFailOnMissingInformer: true},
		Controller: config.Controller{SkipNameValidation: new(true)},
	})
	Expect(err).NotTo(HaveOccurred())

	reconciler := &NamespaceReconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Recorder:  mgr.GetEventRecorder("namespaceclass-controller"),
	}
	Expect(reconciler.SetupWithManager(mgr)).To(Succeed())
	reconciler.watches.gracePeriod = testWatchGracePeriod
	driftWatches = reconciler.watches
	driftCache = &gatedCache{Cache: reconciler.watches.cache, held: map[schema.GroupKind]*controllertest.FakeInformer{}}
	reconciler.watches.cache = driftCache

	mgrCtx, cancelMgr := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(done)
		Expect(mgr.Start(mgrCtx)).To(Succeed())
	}()

	return func() {
		GinkgoHelper()
		cancelMgr()
		Eventually(done).Should(BeClosed())
	}
}

// testWatchGracePeriod replaces the 30-second grace period for drift watches, so that specs can
// wait for it.
const testWatchGracePeriod = 5 * time.Second

// The drift watches of the running manager. Specs read the registry to wait until the watches are
// ready, and use the cache to hold back the watch of a kind.
var (
	driftWatches *watchRegistry
	driftCache   *gatedCache
)

// driftWatchStates returns the state of the drift watch of each kind.
func driftWatchStates() map[schema.GroupKind]watchState {
	driftWatches.mu.Lock()
	defer driftWatches.mu.Unlock()
	return maps.Clone(driftWatches.watched)
}

// expectDriftWatchesReady waits until the drift watch of each object's kind has listed its objects,
// and then until no run is waiting to run again. Until then, a run that repeats every second can
// undo a change to a managed object even when the watch reports nothing, so specs that test the
// watches call this before they change an object. A watch that timed out fails the spec: it may
// never report the change.
func expectDriftWatchesReady(objs ...*unstructured.Unstructured) {
	GinkgoHelper()
	Expect(objs).NotTo(BeEmpty())
	for _, obj := range objs {
		gk := obj.GroupVersionKind().GroupKind()
		Eventually(func(g Gomega) {
			state := driftWatchStates()[gk]
			if state == watchTimedOut {
				StopTrying(fmt.Sprintf("the drift watch of %s did not list its objects within the grace period", gk)).Now()
			}
			g.Expect(state).To(Equal(watchSynced), "the drift watch of %s has not listed its objects", gk)
		}).Should(Succeed())
	}
	settledRequeueCount()
}

// gatedCache is the drift cache, with a way to hold back the watch of a kind. A watch that is held
// back gets a fake informer, which reports no change and has not listed the objects until release
// is called.
type gatedCache struct {
	cache.Cache

	mu   sync.Mutex
	held map[schema.GroupKind]*controllertest.FakeInformer
}

// holdBack must be called before the first watch of the kind is added.
func (c *gatedCache) holdBack(gk schema.GroupKind) (release func()) {
	informer := controllertest.NewFakeInformer()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.held[gk] = informer
	return sync.OnceFunc(informer.Synced)
}

func (c *gatedCache) GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error) {
	c.mu.Lock()
	informer := c.held[obj.GetObjectKind().GroupVersionKind().GroupKind()]
	c.mu.Unlock()
	if informer != nil {
		return informer, nil
	}
	return c.Cache.GetInformer(ctx, obj, opts...)
}

// getFirstFoundEnvTestBinaryDir locates the first binary in the specified path.
// ENVTEST-based tests depend on specific binaries, usually located in paths set by
// controller-runtime. When running tests directly (e.g., via an IDE) without using
// Makefile targets, the 'BinaryAssetsDirectory' must be explicitly configured.
//
// This function streamlines the process by finding the required binaries, similar to
// setting the 'KUBEBUILDER_ASSETS' environment variable. To ensure the binaries are
// properly set up, run 'make setup-envtest' beforehand.
func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		logf.Log.Error(err, "Failed to read directory", "path", basePath)
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}
