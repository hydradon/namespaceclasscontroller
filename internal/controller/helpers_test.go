package controller

import (
	"fmt"
	"maps"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
	"github.com/hydradon/namespaceclasscontroller/internal/inventory"
)

var nameCounter atomic.Int64

// uniqueName returns a name that no other spec uses, so specs never share classes.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, nameCounter.Add(1))
}

// newClass returns a class whose items are the given objects, each written as JSON.
func newClass(name string, items ...*unstructured.Unstructured) *v1alpha1.NamespaceClass {
	class := &v1alpha1.NamespaceClass{ObjectMeta: metav1.ObjectMeta{Name: name}}
	setItems(class, items...)
	return class
}

func setItems(class *v1alpha1.NamespaceClass, items ...*unstructured.Unstructured) {
	GinkgoHelper()
	class.Spec.Resources = nil
	for _, item := range items {
		raw, err := item.MarshalJSON()
		Expect(err).NotTo(HaveOccurred())
		class.Spec.Resources = append(class.Spec.Resources, runtime.RawExtension{Raw: raw})
	}
}

func createClass(name string, items ...*unstructured.Unstructured) *v1alpha1.NamespaceClass {
	GinkgoHelper()
	class := newClass(name, items...)
	Expect(k8sClient.Create(ctx, class)).To(Succeed())
	return class
}

// updateClass replaces the items of the class. class is updated in place.
func updateClass(class *v1alpha1.NamespaceClass, items ...*unstructured.Unstructured) {
	GinkgoHelper()
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(class), class); err != nil {
			return err
		}
		setItems(class, items...)
		return k8sClient.Update(ctx, class)
	})).To(Succeed())
}

// newNamespace returns a namespace with a generated name, labeled with the class when class is
// not empty. Deleted namespaces stay Terminating forever in envtest, so names are never reused.
func newNamespace(class string) *corev1.Namespace {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "ns-"}}
	if class != "" {
		ns.Labels = map[string]string{v1alpha1.ClassLabel: class}
	}
	return ns
}

func createNamespace(class string) *corev1.Namespace {
	GinkgoHelper()
	ns := newNamespace(class)
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	return ns
}

// patchNamespace changes the live namespace with a merge patch and returns its new
// resourceVersion.
func patchNamespace(ns *corev1.Namespace, change func(*corev1.Namespace)) string {
	GinkgoHelper()
	current := &corev1.Namespace{}
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ns), current)).To(Succeed())
	orig := current.DeepCopy()
	change(current)
	Expect(k8sClient.Patch(ctx, current, client.MergeFrom(orig))).To(Succeed())
	return current.ResourceVersion
}

func setClassLabel(ns *corev1.Namespace, class string) string {
	GinkgoHelper()
	return patchNamespace(ns, func(current *corev1.Namespace) {
		if current.Labels == nil {
			current.Labels = map[string]string{}
		}
		current.Labels[v1alpha1.ClassLabel] = class
	})
}

func removeClassLabel(ns *corev1.Namespace) {
	GinkgoHelper()
	patchNamespace(ns, func(current *corev1.Namespace) {
		delete(current.Labels, v1alpha1.ClassLabel)
	})
}

// namespaceVersion returns the live resourceVersion of the namespace, for Eventually and
// Consistently. It changes whenever the controller writes the annotation.
func namespaceVersion(ns *corev1.Namespace) func(g Gomega) string {
	return func(g Gomega) string {
		current := &corev1.Namespace{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ns), current)).To(Succeed())
		return current.ResourceVersion
	}
}

// managedResources returns the live value of the managed-resources annotation.
func managedResources(ns *corev1.Namespace) func(g Gomega) string {
	return func(g Gomega) string {
		current := &corev1.Namespace{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ns), current)).To(Succeed())
		return current.Annotations[v1alpha1.ManagedResourcesAnnotation]
	}
}

// namespaceAnnotations returns the live annotations of the namespace.
func namespaceAnnotations(ns *corev1.Namespace) func(g Gomega) map[string]string {
	return func(g Gomega) map[string]string {
		current := &corev1.Namespace{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ns), current)).To(Succeed())
		return current.Annotations
	}
}

// expectAnnotation waits until the annotation lists exactly the given objects.
func expectAnnotation(ns *corev1.Namespace, objs ...*unstructured.Unstructured) {
	GinkgoHelper()
	Eventually(managedResources(ns)).Should(Equal(inventory.Encode(inventory.RefsOf(objs))))
}

// Field names and data keys used by the test objects.
const (
	specField = "spec"  // the top-level spec field of an object
	sizeField = "size"  // spec.size of a Widget or a Gizmo
	colorKey  = "color" // a data key of test ConfigMaps
)

func newItem(apiVersion, kind, name string, fields map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{}}
	maps.Copy(obj.Object, fields)
	obj.SetAPIVersion(apiVersion)
	obj.SetKind(kind)
	obj.SetName(name)
	return obj
}

func stringMap(values map[string]string) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func configMap(name string, data map[string]string) *unstructured.Unstructured {
	fields := map[string]any{}
	if len(data) > 0 {
		fields["data"] = stringMap(data)
	}
	return newItem("v1", "ConfigMap", name, fields)
}

// networkPolicy allows ingress from cidr to the pods with the given labels (all pods when
// podLabels is empty).
func networkPolicy(name, cidr string, podLabels map[string]string) *unstructured.Unstructured {
	podSelector := map[string]any{}
	if len(podLabels) > 0 {
		podSelector["matchLabels"] = stringMap(podLabels)
	}
	return newItem("networking.k8s.io/v1", "NetworkPolicy", name, map[string]any{
		specField: map[string]any{
			"podSelector": podSelector,
			"policyTypes": []any{"Ingress"},
			"ingress": []any{map[string]any{
				"from": []any{map[string]any{"ipBlock": map[string]any{"cidr": cidr}}},
			}},
		},
	})
}

func roleBinding(name, clusterRole string) *unstructured.Unstructured {
	return newItem("rbac.authorization.k8s.io/v1", "RoleBinding", name, map[string]any{
		"roleRef": map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": clusterRole},
		"subjects": []any{map[string]any{
			"apiGroup": "rbac.authorization.k8s.io", "kind": "Group", "name": "readers",
		}},
	})
}

func widget(name string, size int64) *unstructured.Unstructured {
	return newItem("widget.example.com/v1", "Widget", name, map[string]any{
		specField: map[string]any{sizeField: size},
	})
}

func gadget(name string) *unstructured.Unstructured {
	return newItem("gadget.example.com/v1", "Gadget", name, map[string]any{
		specField: map[string]any{"shape": "round"},
	})
}

func gizmo(name string, size int64) *unstructured.Unstructured {
	return newItem("gizmo.example.com/v1", "Gizmo", name, map[string]any{
		specField: map[string]any{sizeField: size},
	})
}

func sprocket(name string) *unstructured.Unstructured {
	return newItem("sprocket.example.com/v1", "Sprocket", name, map[string]any{
		specField: map[string]any{"teeth": int64(12)},
	})
}

func cog(name string) *unstructured.Unstructured {
	return newItem("cog.example.com/v1", "Cog", name, map[string]any{
		specField: map[string]any{"teeth": int64(8)},
	})
}

// localSubjectAccessReview returns an object of a kind that can be created but never read or
// listed.
func localSubjectAccessReview(name string) *unstructured.Unstructured {
	return newItem("authorization.k8s.io/v1", "LocalSubjectAccessReview", name, map[string]any{
		specField: map[string]any{
			"user":               "alice",
			"resourceAttributes": map[string]any{"verb": "get", "resource": "pods"},
		},
	})
}

// limitRange returns an object of a built-in kind that no other spec uses, so its drift watch is
// added by the spec that uses it.
func limitRange(name string) *unstructured.Unstructured {
	return newItem("v1", "LimitRange", name, map[string]any{
		specField: map[string]any{
			"limits": []any{map[string]any{"type": "Container", "max": map[string]any{"cpu": "1"}}},
		},
	})
}

func service(name string) *unstructured.Unstructured {
	return newItem("v1", "Service", name, map[string]any{
		specField: map[string]any{
			"selector": map[string]any{"app": name},
			"ports":    []any{map[string]any{"port": int64(80)}},
		},
	})
}

func withNamespace(obj *unstructured.Unstructured, namespace string) *unstructured.Unstructured {
	obj.SetNamespace(namespace)
	return obj
}

// widgetCRD, gadgetCRD, gizmoCRD, sprocketCRD and cogCRD return a new object on every call,
// because installing a CRD changes the object passed in. Each test CRD is installed by one spec
// only.
func widgetCRD() *apiextensionsv1.CustomResourceDefinition {
	return testCRD("widget.example.com", "Widget", "widgets")
}

func gadgetCRD() *apiextensionsv1.CustomResourceDefinition {
	return testCRD("gadget.example.com", "Gadget", "gadgets")
}

func gizmoCRD() *apiextensionsv1.CustomResourceDefinition {
	return testCRD("gizmo.example.com", "Gizmo", "gizmos")
}

func sprocketCRD() *apiextensionsv1.CustomResourceDefinition {
	return testCRD("sprocket.example.com", "Sprocket", "sprockets")
}

func cogCRD() *apiextensionsv1.CustomResourceDefinition {
	return testCRD("cog.example.com", "Cog", "cogs")
}

func testCRD(group, kind, plural string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: plural + "." + group},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     kind,
				ListKind: kind + "List",
				Plural:   plural,
				Singular: strings.ToLower(kind),
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    "v1",
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type: "object",
						Properties: map[string]apiextensionsv1.JSONSchemaProps{
							specField: {Type: "object", XPreserveUnknownFields: new(true)},
						},
					},
				},
			}},
		},
	}
}

// installCRD installs the CRD and waits until the API server serves it.
func installCRD(crd *apiextensionsv1.CustomResourceDefinition) {
	GinkgoHelper()
	_, err := envtest.InstallCRDs(cfg, envtest.CRDInstallOptions{
		CRDs: []*apiextensionsv1.CustomResourceDefinition{crd},
	})
	Expect(err).NotTo(HaveOccurred())
}

// getLive reads the object with the kind and name of obj from the namespace.
func getLive(ns *corev1.Namespace, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	live := &unstructured.Unstructured{}
	live.SetGroupVersionKind(obj.GroupVersionKind())
	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: obj.GetName()}, live)
	return live, err
}

// liveObject returns a function for Eventually and Consistently that reads the object.
func liveObject(ns *corev1.Namespace, obj *unstructured.Unstructured) func(g Gomega) *unstructured.Unstructured {
	return func(g Gomega) *unstructured.Unstructured {
		live, err := getLive(ns, obj)
		g.Expect(err).NotTo(HaveOccurred())
		return live
	}
}

// expectCreated waits until the object exists and returns it.
func expectCreated(ns *corev1.Namespace, obj *unstructured.Unstructured) *unstructured.Unstructured {
	GinkgoHelper()
	var live *unstructured.Unstructured
	Eventually(func(g Gomega) {
		live = liveObject(ns, obj)(g)
	}).Should(Succeed())
	return live
}

func isNotFound(ns *corev1.Namespace, obj *unstructured.Unstructured) func() bool {
	return func() bool {
		_, err := getLive(ns, obj)
		return apierrors.IsNotFound(err)
	}
}

func expectDeleted(ns *corev1.Namespace, obj *unstructured.Unstructured) {
	GinkgoHelper()
	Eventually(isNotFound(ns, obj)).Should(BeTrue(), "%s %s should be deleted", obj.GetKind(), obj.GetName())
}

func dataOf(obj *unstructured.Unstructured) map[string]string {
	data, _, _ := unstructured.NestedStringMap(obj.Object, "data")
	return data
}

func liveData(ns *corev1.Namespace, obj *unstructured.Unstructured) func(g Gomega) map[string]string {
	return func(g Gomega) map[string]string {
		return dataOf(liveObject(ns, obj)(g))
	}
}

func liveLabels(ns *corev1.Namespace, obj *unstructured.Unstructured) func(g Gomega) map[string]string {
	return func(g Gomega) map[string]string {
		return liveObject(ns, obj)(g).GetLabels()
	}
}

// liveSize returns spec.size of the live object (Widget, Gizmo).
func liveSize(ns *corev1.Namespace, obj *unstructured.Unstructured) func(g Gomega) int64 {
	return func(g Gomega) int64 {
		size, _, err := unstructured.NestedInt64(liveObject(ns, obj)(g).Object, specField, sizeField)
		g.Expect(err).NotTo(HaveOccurred())
		return size
	}
}

// otherManager is the field manager of the changes that specs make in place of a user.
const otherManager = "test-user"

// patchAsUser changes the live object with a merge patch sent by otherManager. obj must be the
// live object; it is updated in place, including its new resourceVersion.
func patchAsUser(obj *unstructured.Unstructured, change func(*unstructured.Unstructured)) {
	GinkgoHelper()
	orig := obj.DeepCopy()
	change(obj)
	Expect(k8sClient.Patch(ctx, obj, client.MergeFrom(orig), client.FieldOwner(otherManager))).To(Succeed())
}

// resourceVersions returns the live resourceVersions of the namespace and of the objects in it,
// keyed by kind and name.
func resourceVersions(ns *corev1.Namespace, objs ...*unstructured.Unstructured) func(g Gomega) map[string]string {
	return func(g Gomega) map[string]string {
		versions := map[string]string{"Namespace": namespaceVersion(ns)(g)}
		for _, obj := range objs {
			versions[obj.GetKind()+"/"+obj.GetName()] = liveObject(ns, obj)(g).GetResourceVersion()
		}
		return versions
	}
}

// ownedBy matches the owner reference the controller sets: the class, marked as controller.
func ownedBy(class *v1alpha1.NamespaceClass) gomegatypes.GomegaMatcher {
	return SatisfyAll(
		HaveField("APIVersion", v1alpha1.GroupVersion.String()),
		HaveField("Kind", "NamespaceClass"),
		HaveField("Name", class.Name),
		HaveField("UID", class.UID),
		HaveField("Controller", HaveValue(BeTrue())),
	)
}

// eventsAbout lists the events whose regarding object is obj. Events about cluster-scoped
// objects (Namespaces, NamespaceClasses) are stored in the default namespace.
func eventsAbout(obj client.Object) func(g Gomega) []eventsv1.Event {
	return func(g Gomega) []eventsv1.Event {
		list := &eventsv1.EventList{}
		g.Expect(k8sClient.List(ctx, list, client.InNamespace(metav1.NamespaceDefault))).To(Succeed())
		var found []eventsv1.Event
		for _, ev := range list.Items {
			if ev.Regarding.UID == obj.GetUID() {
				found = append(found, ev)
			}
		}
		return found
	}
}

func eventWith(eventType, reason, noteSubstr string) gomegatypes.GomegaMatcher {
	return SatisfyAll(
		HaveField("Type", eventType),
		HaveField("Reason", reason),
		HaveField("Note", ContainSubstring(noteSubstr)),
	)
}

// eventSeries returns the series count of each event about obj, by event name. The count is 0 for
// an event that happened once. When the same event happens again, the recorder does not create a
// new event: it writes a series with count 2 to the existing event right away, and writes later
// repeats only every 30 minutes.
func eventSeries(obj client.Object) func(g Gomega) map[string]int32 {
	return func(g Gomega) map[string]int32 {
		counts := map[string]int32{}
		for _, ev := range eventsAbout(obj)(g) {
			counts[ev.Name] = 0
			if ev.Series != nil {
				counts[ev.Name] = ev.Series.Count
			}
		}
		return counts
	}
}

// settledEvents waits until the events about obj stay the same between two polls one second
// apart, and returns their series counts.
func settledEvents(obj client.Object) map[string]int32 {
	GinkgoHelper()
	var last map[string]int32
	Eventually(func(g Gomega) {
		previous := last
		last = eventSeries(obj)(g)
		g.Expect(last).To(Equal(previous))
	}).WithPolling(time.Second).Should(Succeed())
	return last
}

// expectEvent waits for an event about obj and returns it. The recorder merges repeated events
// into one, so specs check that an event exists and never count events.
func expectEvent(obj client.Object, eventType, reason, noteSubstr string) eventsv1.Event {
	GinkgoHelper()
	var found []eventsv1.Event
	Eventually(eventsAbout(obj)).Should(ContainElement(eventWith(eventType, reason, noteSubstr), &found))
	return found[0]
}

// requeueAfterCount returns how many runs of the controller have asked to run again after a delay
// (controller_runtime_reconcile_total with result requeue_after), over all namespaces.
func requeueAfterCount(g Gomega) float64 {
	families, err := metrics.Registry.Gather()
	g.Expect(err).NotTo(HaveOccurred())
	for _, family := range families {
		if family.GetName() != "controller_runtime_reconcile_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["controller"] == "namespaceclass" && labels["result"] == "requeue_after" {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// settledRequeueCount waits until no run has asked to run again for longer than the one-second
// delay, and returns requeueAfterCount.
func settledRequeueCount() float64 {
	GinkgoHelper()
	last := -1.0
	Eventually(func(g Gomega) {
		previous := last
		last = requeueAfterCount(g)
		g.Expect(last).To(Equal(previous))
	}).WithPolling(1500 * time.Millisecond).Should(Succeed())
	return last
}
