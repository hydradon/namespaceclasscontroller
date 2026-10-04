package controller

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
)

const testFinalizer = "test.namespaceclass.akuity.io/hold"

var _ = Describe("Namespace controller", func() {
	It("creates the class's objects in a namespace that opts in", func() {
		data := map[string]string{"color": "green"}
		settings := configMap("settings", data)
		ingress := networkPolicy("ingress", "10.0.0.0/8", nil)
		class := createClass(uniqueName("opt-in"), settings, ingress)
		ns := createNamespace(class.Name)

		for _, obj := range []*unstructured.Unstructured{settings, ingress} {
			live := expectCreated(ns, obj)
			Expect(live.GetNamespace()).To(Equal(ns.Name))
			Expect(live.GetLabels()).To(HaveKeyWithValue(v1alpha1.ManagedByClassLabel, class.Name))
			Expect(live.GetOwnerReferences()).To(ConsistOf(ownedBy(class)))
		}
		Expect(dataOf(expectCreated(ns, settings))).To(Equal(data))

		Eventually(managedResources(ns)).Should(Equal(
			`[{"kind":"ConfigMap","name":"settings"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]`))
		created := expectEvent(ns, corev1.EventTypeNormal, ReasonCreated, "ConfigMap/settings")
		Expect(created.Related).To(HaveField("Name", "settings"))
		expectEvent(ns, corev1.EventTypeNormal, ReasonCreated, "NetworkPolicy.networking.k8s.io/ingress")
	})

	It("applies a class that is created after the namespace", func() {
		className := uniqueName("later")
		ns := createNamespace(className)
		expectEvent(ns, corev1.EventTypeWarning, ReasonClassNotFound, fmt.Sprintf("%q does not exist", className))

		later := configMap("later", nil)
		createClass(className, later)
		expectCreated(ns, later)
		expectAnnotation(ns, later)
	})

	It("follows edits to the class", func() {
		one := configMap("one", map[string]string{"v": "1"})
		two := configMap("two", map[string]string{"v": "1", "extra": "x"})
		class := createClass(uniqueName("edit"), one, two)
		ns := createNamespace(class.Name)
		oneVersion := expectCreated(ns, one).GetResourceVersion()
		twoVersion := expectCreated(ns, two).GetResourceVersion()
		expectAnnotation(ns, one, two)

		By("adding an item: only the new object is written")
		three := configMap("three", nil)
		updateClass(class, one, two, three)
		expectCreated(ns, three)
		expectEvent(ns, corev1.EventTypeNormal, ReasonCreated, "ConfigMap/three")
		expectAnnotation(ns, one, two, three)
		Consistently(func(g Gomega) {
			g.Expect(liveObject(ns, one)(g).GetResourceVersion()).To(Equal(oneVersion))
			g.Expect(liveObject(ns, two)(g).GetResourceVersion()).To(Equal(twoVersion))
			g.Expect(eventsAbout(ns)(g)).NotTo(ContainElement(HaveField("Reason", ReasonUpdated)))
		}).Should(Succeed())

		By("changing an item and removing a field from another item")
		oneChanged := configMap("one", map[string]string{"v": "2"})
		twoWithoutExtra := configMap("two", map[string]string{"v": "1"})
		updateClass(class, oneChanged, twoWithoutExtra, three)
		Eventually(liveData(ns, one)).Should(Equal(map[string]string{"v": "2"}))
		Eventually(liveData(ns, two)).Should(Equal(map[string]string{"v": "1"}))
		expectEvent(ns, corev1.EventTypeNormal, ReasonUpdated, "ConfigMap/one")
		expectEvent(ns, corev1.EventTypeNormal, ReasonUpdated, "ConfigMap/two")

		By("removing an item")
		updateClass(class, oneChanged, twoWithoutExtra)
		expectDeleted(ns, three)
		expectEvent(ns, corev1.EventTypeNormal, ReasonDeleted, "ConfigMap/three")
		expectAnnotation(ns, one, two)
	})

	It("moves an object that both classes define to the new class without re-creating it", func() {
		sharedA := networkPolicy("shared", "10.0.0.0/8", map[string]string{"app": "a"})
		onlyA := configMap("only-a", nil)
		sharedB := networkPolicy("shared", "192.168.0.0/16", nil)
		onlyB := configMap("only-b", nil)
		classA := createClass(uniqueName("switch-a"), sharedA, onlyA)
		classB := createClass(uniqueName("switch-b"), sharedB, onlyB)
		ns := createNamespace(classA.Name)
		before := expectCreated(ns, sharedA)
		expectCreated(ns, onlyA)
		expectAnnotation(ns, sharedA, onlyA)

		By("letting another field manager add a label")
		orig := before.DeepCopy()
		labels := before.GetLabels()
		labels["team"] = "platform"
		before.SetLabels(labels)
		Expect(k8sClient.Patch(ctx, before, client.MergeFrom(orig), client.FieldOwner(otherManager))).To(Succeed())

		By("switching to class B")
		setClassLabel(ns, classB.Name)
		Eventually(liveLabels(ns, sharedB)).Should(HaveKeyWithValue(v1alpha1.ManagedByClassLabel, classB.Name))
		after := liveObject(ns, sharedB)(Default)
		Expect(after.GetUID()).To(Equal(before.GetUID()))
		Expect(after.GetOwnerReferences()).To(ConsistOf(ownedBy(classB)))
		Expect(after.GetLabels()).To(HaveKeyWithValue("team", "platform"))
		spec, _, err := unstructured.NestedMap(after.Object, specField)
		Expect(err).NotTo(HaveOccurred())
		Expect(spec).To(HaveKeyWithValue("podSelector", BeEmpty()), "the pod labels only class A set are removed")
		Expect(spec).To(HaveKeyWithValue("ingress", sharedB.Object[specField].(map[string]any)["ingress"]))

		expectDeleted(ns, onlyA)
		expectCreated(ns, onlyB)
		expectAnnotation(ns, sharedB, onlyB)
	})

	It("deletes the objects and the annotation when the label is removed", func() {
		first := configMap("first", nil)
		ingress := networkPolicy("ingress", "10.0.0.0/8", nil)
		class := createClass(uniqueName("opt-out"), first, ingress)
		ns := createNamespace(class.Name)
		expectCreated(ns, first)
		expectCreated(ns, ingress)
		expectAnnotation(ns, first, ingress)

		removeClassLabel(ns)
		expectDeleted(ns, first)
		expectDeleted(ns, ingress)
		expectEvent(ns, corev1.EventTypeNormal, ReasonDeleted, "ConfigMap/first")
		expectEvent(ns, corev1.EventTypeNormal, ReasonDeleted, "NetworkPolicy.networking.k8s.io/ingress")
		Eventually(namespaceAnnotations(ns)).ShouldNot(HaveKey(v1alpha1.ManagedResourcesAnnotation))
	})

	It("does not delete an object that was taken out of management", func() {
		kept := configMap("kept", nil)
		takenOver := configMap("taken-over", nil)
		class := createClass(uniqueName("take-over"), kept, takenOver)
		ns := createNamespace(class.Name)
		expectCreated(ns, kept)
		live := expectCreated(ns, takenOver)
		expectAnnotation(ns, kept, takenOver)

		By("removing the controller label and the owner reference")
		orig := live.DeepCopy()
		labels := live.GetLabels()
		delete(labels, v1alpha1.ManagedByClassLabel)
		live.SetLabels(labels)
		live.SetOwnerReferences(nil)
		Expect(k8sClient.Patch(ctx, live, client.MergeFrom(orig))).To(Succeed())

		By("removing the object from the class")
		updateClass(class, kept)
		expectAnnotation(ns, kept)
		Consistently(func(g Gomega) types.UID {
			return liveObject(ns, takenOver)(g).GetUID()
		}).Should(Equal(live.GetUID()))
	})

	DescribeTable("a label that names no class changes nothing",
		func(value, note string) {
			kept := configMap("kept", nil)
			class := createClass(uniqueName("label"), kept)
			ns := createNamespace(class.Name)
			expectCreated(ns, kept)
			expectAnnotation(ns, kept)

			version := setClassLabel(ns, value)
			expectEvent(ns, corev1.EventTypeWarning, ReasonClassNotFound, note)
			Consistently(namespaceVersion(ns)).Should(Equal(version))
			expectCreated(ns, kept)
		},
		Entry("empty value", "", "is empty"),
		Entry("typo", "no-such-class", `"no-such-class" does not exist`),
	)

	It("leaves the objects in place when the class is deleted", func() {
		stays := configMap("stays", nil)
		class := createClass(uniqueName("deleted"), stays)
		ns := createNamespace(class.Name)
		expectCreated(ns, stays)
		expectAnnotation(ns, stays)
		version := namespaceVersion(ns)(Default)

		// envtest has no garbage collector, so this only shows what the controller itself does.
		Expect(k8sClient.Delete(ctx, class)).To(Succeed())
		expectEvent(ns, corev1.EventTypeWarning, ReasonClassNotFound, fmt.Sprintf("%q does not exist", class.Name))
		Consistently(func(g Gomega) {
			g.Expect(liveObject(ns, stays)(g).GetOwnerReferences()).To(ConsistOf(ownedBy(class)))
			g.Expect(namespaceVersion(ns)(g)).To(Equal(version))
		}).Should(Succeed())
	})

	It("does not apply a class that is being deleted", func() {
		first := configMap("first", nil)
		second := configMap("second", nil)
		class := newClass(uniqueName("deleting"), first, second)
		class.Finalizers = []string{testFinalizer}
		Expect(k8sClient.Create(ctx, class)).To(Succeed())
		DeferCleanup(func() {
			Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
				current := &v1alpha1.NamespaceClass{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(class), current); err != nil {
					return client.IgnoreNotFound(err)
				}
				current.Finalizers = nil
				return k8sClient.Update(ctx, current)
			})).To(Succeed())
		})
		ns := createNamespace(class.Name)
		firstLive := expectCreated(ns, first)
		expectCreated(ns, second)
		expectAnnotation(ns, first, second)

		By("deleting the class while a finalizer holds it")
		Expect(k8sClient.Delete(ctx, class)).To(Succeed())
		expectEvent(ns, corev1.EventTypeWarning, ReasonClassNotFound, "is being deleted")

		By("deleting a created object and editing the class")
		Expect(k8sClient.Delete(ctx, firstLive)).To(Succeed())
		third := configMap("third", nil)
		updateClass(class, first, second, third)
		// The related object of the event is the class at the version that was just written, so
		// this event proves that a run happened after the edit.
		Eventually(eventsAbout(ns)).Should(ContainElement(SatisfyAll(
			eventWith(corev1.EventTypeWarning, ReasonClassNotFound, "is being deleted"),
			HaveField("Related.ResourceVersion", class.ResourceVersion),
		)))
		Consistently(func(g Gomega) {
			g.Expect(isNotFound(ns, first)()).To(BeTrue(), "first should not be re-created")
			g.Expect(isNotFound(ns, third)()).To(BeTrue(), "third should not be created")
		}).Should(Succeed())
		Expect(liveObject(ns, second)(Default).GetOwnerReferences()).To(ConsistOf(ownedBy(class)))
	})

	It("leaves an existing object alone and still finishes a switch", func() {
		classData := map[string]string{"owner": "class"}
		userData := map[string]string{"owner": "user"}
		onlyA := configMap("only-a", nil)
		classA := createClass(uniqueName("conflict-a"), onlyA)
		settings := configMap("settings", classData)
		onlyB := configMap("only-b", map[string]string{"v": "1"})
		classB := createClass(uniqueName("conflict-b"), settings, onlyB)
		ns := createNamespace(classA.Name)
		expectCreated(ns, onlyA)
		expectAnnotation(ns, onlyA)

		By("creating a ConfigMap that the controller did not create")
		users := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: ns.Name},
			Data:       userData,
		}
		Expect(k8sClient.Create(ctx, users)).To(Succeed())

		By("switching to class B, which also defines settings")
		expectDriftWatchesReady(onlyA) // the deletion of only-a below must start a run
		setClassLabel(ns, classB.Name)
		expectCreated(ns, onlyB)
		expectDeleted(ns, onlyA)
		expectEvent(ns, corev1.EventTypeWarning, ReasonConflict, "ConfigMap/settings")
		expectAnnotation(ns, onlyB)

		live := liveObject(ns, settings)(Default)
		Expect(live.GetResourceVersion()).To(Equal(users.ResourceVersion))
		Expect(dataOf(live)).To(Equal(userData))
		Expect(live.GetLabels()).NotTo(HaveKey(v1alpha1.ManagedByClassLabel))
		Expect(live.GetOwnerReferences()).To(BeEmpty())
		Expect(live.GetManagedFields()).NotTo(ContainElement(HaveField("Manager", fieldManager)))

		By("not retrying: no annotation write, no change to the user's object, no new or repeated event")
		expectEvent(ns, corev1.EventTypeNormal, ReasonCreated, "ConfigMap/only-b")
		expectEvent(ns, corev1.EventTypeNormal, ReasonDeleted, "ConfigMap/only-a")
		// The annotation checked above was the last write of the switch.
		version := namespaceVersion(ns)(Default)
		// Deleting only-a starts one more run (drift correction). That run finds the conflict
		// again at the final version of the namespace, so its Conflict event is a new event.
		conflictAtVersion := SatisfyAll(
			eventWith(corev1.EventTypeWarning, ReasonConflict, "ConfigMap/settings"),
			HaveField("Regarding.ResourceVersion", version),
		)
		Eventually(eventsAbout(ns)).Should(ContainElement(conflictAtVersion))
		events := settledEvents(ns)
		// Another run with an unchanged namespace would repeat the Conflict event, and a repeated
		// event gets a series right away.
		Expect(events).To(HaveEach(BeZero()), "an event about the namespace was recorded twice")
		Consistently(func(g Gomega) {
			g.Expect(namespaceVersion(ns)(g)).To(Equal(version))
			g.Expect(liveObject(ns, settings)(g).GetResourceVersion()).To(Equal(users.ResourceVersion))
			g.Expect(eventSeries(ns)(g)).To(Equal(events))
		}).Should(Succeed())

		By("writing nothing in a later run while the conflict lasts")
		updateClass(classB, onlyB, settings) // the same objects in a new order: a new generation, so a new run
		// The new run repeats the Conflict event of the run above, so that event gets a series.
		Eventually(eventsAbout(ns)).Should(ContainElement(SatisfyAll(
			conflictAtVersion,
			HaveField("Series.Count", BeEquivalentTo(2)),
		)))
		Consistently(namespaceVersion(ns)).Should(Equal(version))

		By("creating the class's object after the user's object is gone and the class changes")
		Expect(k8sClient.Delete(ctx, users)).To(Succeed())
		updateClass(classB, settings, configMap("only-b", map[string]string{"v": "2"}))
		Eventually(liveLabels(ns, settings)).Should(HaveKeyWithValue(v1alpha1.ManagedByClassLabel, classB.Name))
		Expect(liveData(ns, settings)(Default)).To(Equal(classData))
		expectAnnotation(ns, settings, onlyB)
	})

	DescribeTable("an invalid class changes nothing until it is fixed",
		func(problem string, invalid ...*unstructured.Unstructured) {
			ok := configMap("ok", map[string]string{"v": "1"})
			class := createClass(uniqueName("invalid"), ok)
			ns := createNamespace(class.Name)
			expectCreated(ns, ok)
			expectAnnotation(ns, ok)
			version := namespaceVersion(ns)(Default)

			okChanged := configMap("ok", map[string]string{"v": "2"})
			updateClass(class, append([]*unstructured.Unstructured{okChanged}, invalid...)...)
			expectEvent(ns, corev1.EventTypeWarning, ReasonInvalidClass, problem)
			expectEvent(class, corev1.EventTypeWarning, ReasonInvalidClass, problem)
			Consistently(func(g Gomega) {
				g.Expect(liveData(ns, ok)(g)).To(Equal(map[string]string{"v": "1"}))
				g.Expect(namespaceVersion(ns)(g)).To(Equal(version))
			}).Should(Succeed())

			By("fixing the class")
			updateClass(class, okChanged)
			Eventually(liveData(ns, ok)).Should(Equal(map[string]string{"v": "2"}))
		},
		Entry("two items with the same kind and name", "duplicates",
			configMap("twice", nil), configMap("twice", nil)),
		Entry("metadata.namespace set", "metadata.namespace must be empty",
			withNamespace(configMap("elsewhere", nil), "other")),
		Entry("cluster-scoped kind", "cluster-scoped",
			newItem("rbac.authorization.k8s.io/v1", "ClusterRole", "reader", nil)),
		Entry("ServiceAccount default", "created by Kubernetes",
			newItem("v1", "ServiceAccount", "default", nil)),
	)

	It("waits for a kind whose CRD is not installed and applies the class once it is", func() {
		withGadget := configMap("with-gadget", nil)
		g1 := gadget("g1")
		class := createClass(uniqueName("gadget"), withGadget, g1)
		ns := createNamespace(class.Name)
		expectEvent(ns, corev1.EventTypeWarning, ReasonApplyFailed, "Gadget")
		Consistently(func(g Gomega) {
			_, err := getLive(ns, withGadget)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "nothing should be created")
			g.Expect(managedResources(ns)(g)).To(BeEmpty())
		}).Should(Succeed())

		installCRD(gadgetCRD())
		expectCreated(ns, withGadget)
		Expect(expectCreated(ns, g1).GetOwnerReferences()).To(ConsistOf(ownedBy(class)))
		expectAnnotation(ns, withGadget, g1)
	})

	It("applies the other items and deletes nothing when one item is rejected", func() {
		cmA := configMap("cm-a", map[string]string{"v": "1"})
		rb := roleBinding("rb", "view")
		cmB := configMap("cm-b", nil)
		class := createClass(uniqueName("partial"), cmA, rb, cmB)
		ns := createNamespace(class.Name)
		expectCreated(ns, cmA)
		rbVersion := expectCreated(ns, rb).GetResourceVersion()
		expectCreated(ns, cmB)
		expectAnnotation(ns, cmA, rb, cmB)

		By("changing the roleRef, which the API server does not allow")
		cmAChanged := configMap("cm-a", map[string]string{"v": "2"})
		cmC := configMap("cm-c", nil)
		updateClass(class, cmAChanged, roleBinding("rb", "edit"), cmC)
		Eventually(liveData(ns, cmA)).Should(Equal(map[string]string{"v": "2"}))
		expectCreated(ns, cmC)
		failed := expectEvent(ns, corev1.EventTypeWarning, ReasonApplyFailed, "RoleBinding.rbac.authorization.k8s.io/rb")
		Expect(len(failed.Note)).To(BeNumerically("<=", 1024))
		expectAnnotation(ns, cmA, rb, cmB, cmC)
		Consistently(func(g Gomega) {
			g.Expect(isNotFound(ns, cmB)()).To(BeFalse(), "cm-b should not be deleted")
			g.Expect(liveObject(ns, rb)(g).GetResourceVersion()).To(Equal(rbVersion))
		}).Should(Succeed())

		By("renaming the RoleBinding")
		renamed := roleBinding("rb-edit", "edit")
		updateClass(class, cmAChanged, renamed, cmC)
		expectCreated(ns, renamed)
		expectDeleted(ns, rb)
		expectDeleted(ns, cmB)
		expectAnnotation(ns, cmA, renamed, cmC)
	})

	It("manages a custom resource whose CRD is installed while the controller runs", func() {
		installCRD(widgetCRD())
		w1 := widget("w1", 3)
		class := createClass(uniqueName("widget"), w1)
		ns := createNamespace(class.Name)
		live := expectCreated(ns, w1)
		Expect(live.GetLabels()).To(HaveKeyWithValue(v1alpha1.ManagedByClassLabel, class.Name))
		Expect(live.GetOwnerReferences()).To(ConsistOf(ownedBy(class)))
		expectAnnotation(ns, w1)

		updateClass(class, widget("w1", 5))
		Eventually(liveSize(ns, w1)).Should(Equal(int64(5)))
		expectEvent(ns, corev1.EventTypeNormal, ReasonUpdated, "Widget.widget.example.com/w1")

		updateClass(class)
		expectDeleted(ns, w1)
		Eventually(namespaceAnnotations(ns)).ShouldNot(HaveKey(v1alpha1.ManagedResourcesAnnotation))
	})

	It("changes nothing in a namespace that is being deleted", func() {
		cm := configMap("cm", map[string]string{"v": "1"})
		class := createClass(uniqueName("terminating"), cm)
		ns := createNamespace(class.Name)
		cmVersion := expectCreated(ns, cm).GetResourceVersion()
		expectAnnotation(ns, cm)
		// A second namespace of the class that is not being deleted. It gets the object that the class
		// edit below adds, which shows that the edit started runs.
		other := createNamespace(class.Name)
		expectCreated(other, cm)

		// envtest has no namespace controller, so the namespace stays Terminating.
		Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		var version string
		Eventually(func(g Gomega) {
			current := &corev1.Namespace{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ns), current)).To(Succeed())
			g.Expect(current.DeletionTimestamp).NotTo(BeNil())
			version = current.ResourceVersion
		}).Should(Succeed())

		added := configMap("added", nil)
		updateClass(class, configMap("cm", map[string]string{"v": "2"}), added)
		expectCreated(other, added)
		Consistently(func(g Gomega) {
			g.Expect(liveObject(ns, cm)(g).GetResourceVersion()).To(Equal(cmVersion))
			g.Expect(namespaceVersion(ns)(g)).To(Equal(version))
		}).Should(Succeed())
	})

	It("catches up with changes made while the controller was stopped", func() {
		p1 := configMap("p1", nil)
		classP := createClass(uniqueName("restart-p"), p1)
		q1 := configMap("q1", nil)
		classQ := createClass(uniqueName("restart-q"), q1)
		c1 := configMap("c1", nil)
		classC := createClass(uniqueName("restart-c"), c1)
		labelRemoved := createNamespace(classP.Name)
		switched := createNamespace(classP.Name)
		objectDeleted := createNamespace(classP.Name)
		classDeleted := createNamespace(classC.Name)
		for _, ns := range []*corev1.Namespace{labelRemoved, switched, objectDeleted} {
			expectCreated(ns, p1)
			expectAnnotation(ns, p1)
		}
		p1InObjectDeleted := liveObject(objectDeleted, p1)(Default)
		c1Version := expectCreated(classDeleted, c1).GetResourceVersion()
		expectAnnotation(classDeleted, c1)

		By("stopping the controller")
		stopManager()
		stopManager = nil
		DeferCleanup(func() {
			if stopManager == nil {
				stopManager = startManager()
			}
		})

		removeClassLabel(labelRemoved)
		setClassLabel(switched, classQ.Name)
		Expect(k8sClient.Delete(ctx, p1InObjectDeleted)).To(Succeed())
		Expect(k8sClient.Delete(ctx, classC)).To(Succeed())
		classDeletedVersion := namespaceVersion(classDeleted)(Default)

		By("starting a new controller")
		stopManager = startManager()

		expectDeleted(labelRemoved, p1)
		Eventually(namespaceAnnotations(labelRemoved)).ShouldNot(HaveKey(v1alpha1.ManagedResourcesAnnotation))
		expectCreated(switched, q1)
		expectDeleted(switched, p1)
		expectAnnotation(switched, q1)
		Expect(expectCreated(objectDeleted, p1).GetUID()).NotTo(Equal(p1InObjectDeleted.GetUID()))
		expectEvent(classDeleted, corev1.EventTypeWarning, ReasonClassNotFound, classC.Name)
		Consistently(func(g Gomega) {
			g.Expect(liveObject(classDeleted, c1)(g).GetResourceVersion()).To(Equal(c1Version))
			g.Expect(namespaceVersion(classDeleted)(g)).To(Equal(classDeletedVersion))
		}).Should(Succeed())
	})

	It("rewrites an annotation it cannot read and deletes nothing", func() {
		kept := configMap("kept", nil)
		dropped := configMap("dropped", nil)
		class := createClass(uniqueName("unreadable"), kept, dropped)
		ns := createNamespace(class.Name)
		expectCreated(ns, kept)
		droppedUID := expectCreated(ns, dropped).GetUID()
		expectAnnotation(ns, kept, dropped)

		patchNamespace(ns, func(current *corev1.Namespace) {
			current.Annotations[v1alpha1.ManagedResourcesAnnotation] = "not-json"
		})
		added := configMap("added", nil)
		updateClass(class, kept, added)
		expectEvent(ns, corev1.EventTypeWarning, ReasonInvalidAnnotation, v1alpha1.ManagedResourcesAnnotation)
		expectCreated(ns, added)
		expectAnnotation(ns, kept, added)
		Consistently(func(g Gomega) types.UID {
			return liveObject(ns, dropped)(g).GetUID()
		}).Should(Equal(droppedUID))
	})
})
