package controller

import (
	"maps"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
)

var _ = Describe("Drift correction", func() {
	It("re-creates a managed object that was deleted", func() {
		data := map[string]string{colorKey: "green"}
		settings := configMap("settings", data)
		class := createClass(uniqueName("drift-delete"), settings)
		ns := createNamespace(class.Name)
		before := expectCreated(ns, settings)
		expectAnnotation(ns, settings)
		expectDriftWatchesReady(settings)

		Expect(k8sClient.Delete(ctx, before)).To(Succeed())
		var after *unstructured.Unstructured
		Eventually(func(g Gomega) {
			after = liveObject(ns, settings)(g)
			g.Expect(after.GetUID()).NotTo(Equal(before.GetUID()))
		}).Should(Succeed())
		Expect(dataOf(after)).To(Equal(data))
		Expect(after.GetLabels()).To(HaveKeyWithValue(v1alpha1.ManagedByClassLabel, class.Name))
		Expect(after.GetOwnerReferences()).To(ConsistOf(ownedBy(class)))
		Eventually(eventsAbout(ns)).Should(ContainElement(SatisfyAll(
			eventWith(corev1.EventTypeNormal, ReasonCreated, "ConfigMap/settings"),
			HaveField("Related.UID", after.GetUID()),
		)))
	})

	It("puts back a field that another field manager changed and keeps a field the class does not set", func() {
		classData := map[string]string{colorKey: "blue"}
		settings := configMap("settings", classData)
		class := createClass(uniqueName("drift-edit"), settings)
		ns := createNamespace(class.Name)
		live := expectCreated(ns, settings)
		expectAnnotation(ns, settings)
		expectDriftWatchesReady(settings)

		const comment = "added by a user"
		patchAsUser(live, func(obj *unstructured.Unstructured) {
			Expect(unstructured.SetNestedField(obj.Object, "red", "data", colorKey)).To(Succeed())
			Expect(unstructured.SetNestedField(obj.Object, comment, "data", "comment")).To(Succeed())
		})
		want := maps.Clone(classData)
		want["comment"] = comment
		Eventually(liveData(ns, settings)).Should(Equal(want))
		Expect(liveObject(ns, settings)(Default).GetUID()).To(Equal(live.GetUID()))
		expectEvent(ns, corev1.EventTypeNormal, ReasonUpdated, "ConfigMap/settings")
	})

	It("puts the label back when only the label is removed, and leaves the object alone when the owner reference is removed too", func() {
		settings := configMap("settings", nil)
		class := createClass(uniqueName("drift-label"), settings)
		ns := createNamespace(class.Name)
		live := expectCreated(ns, settings)
		expectAnnotation(ns, settings)
		expectDriftWatchesReady(settings)
		withoutLabel := func(obj *unstructured.Unstructured) {
			labels := obj.GetLabels()
			delete(labels, v1alpha1.ManagedByClassLabel)
			obj.SetLabels(labels)
		}

		By("removing only the controller label")
		patchAsUser(live, withoutLabel)
		Eventually(liveLabels(ns, settings)).Should(HaveKeyWithValue(v1alpha1.ManagedByClassLabel, class.Name))
		Expect(liveObject(ns, settings)(Default).GetUID()).To(Equal(live.GetUID()))

		By("removing the controller label and the owner reference")
		live = liveObject(ns, settings)(Default)
		patchAsUser(live, func(obj *unstructured.Unstructured) {
			withoutLabel(obj)
			obj.SetOwnerReferences(nil)
		})
		expectEvent(ns, corev1.EventTypeWarning, ReasonConflict, "ConfigMap/settings")
		expectAnnotation(ns)

		By("not starting a run when the object changes again")
		// Only objects with the controller label are watched. A run would record a new Conflict
		// event, because the event names the object's new resourceVersion.
		events := settledEvents(ns)
		version := namespaceVersion(ns)(Default)
		patchAsUser(live, func(obj *unstructured.Unstructured) {
			Expect(unstructured.SetNestedField(obj.Object, "user", "data", "owner")).To(Succeed())
		})
		Consistently(func(g Gomega) {
			g.Expect(liveObject(ns, settings)(g).GetResourceVersion()).To(Equal(live.GetResourceVersion()))
			g.Expect(namespaceVersion(ns)(g)).To(Equal(version))
			g.Expect(eventSeries(ns)(g)).To(Equal(events))
		}).Should(Succeed())
	})

	It("restores an object that is deleted before the watch for its kind is ready", func() {
		s1 := sprocket("s1")
		// The watch of this kind reports no event at all, so only the runs that repeat while the
		// watch is not ready can restore the object.
		release := driftCache.holdBack(s1.GroupVersionKind().GroupKind())
		DeferCleanup(release)
		installCRD(sprocketCRD())
		class := createClass(uniqueName("drift-early"), s1)
		ns := createNamespace(class.Name)
		live := expectCreated(ns, s1)

		Expect(k8sClient.Delete(ctx, live)).To(Succeed())
		Eventually(func(g Gomega) types.UID {
			return liveObject(ns, s1)(g).GetUID()
		}).ShouldNot(Equal(live.GetUID()))

		By("letting the watch list the objects")
		release()
		expectDriftWatchesReady(s1)
	})

	It("reports a new watch as ready while the watch of another kind cannot list its objects", func() {
		lsar := localSubjectAccessReview("check")
		lsarKind := lsar.GroupVersionKind().GroupKind()
		createOnly := createClass(uniqueName("create-only"), lsar)
		createNamespace(createOnly.Name)
		Eventually(driftWatchStates).Should(HaveKey(lsarKind))

		limits := limitRange("limits")
		class := createClass(uniqueName("after-create-only"), limits)
		ns := createNamespace(class.Name)
		expectCreated(ns, limits)
		// Synced, not timed out: the watch is ready because its own informer has listed the
		// objects, even though the informer of LocalSubjectAccessReview never can.
		Eventually(driftWatchStates).Should(HaveKeyWithValue(limits.GroupVersionKind().GroupKind(), watchSynced))

		By("not running the second namespace again and again")
		count := settledRequeueCount()
		Consistently(requeueAfterCount).Should(Equal(count))
	})

	It("stops repeating the runs for a watch that is not ready within the grace period", func() {
		c1 := cog("c1")
		gk := c1.GroupVersionKind().GroupKind()
		// Never released while the spec runs: the watch never lists the objects.
		DeferCleanup(driftCache.holdBack(gk))
		installCRD(cogCRD())
		before := requeueAfterCount(Default)
		class := createClass(uniqueName("drift-stuck"), c1)
		ns := createNamespace(class.Name)
		expectCreated(ns, c1)
		Eventually(requeueAfterCount).Should(BeNumerically(">", before), "the run should be repeated while the watch is not ready")

		By("waiting for the grace period")
		Eventually(driftWatchStates).Should(HaveKeyWithValue(gk, watchTimedOut))
		count := settledRequeueCount()
		Consistently(requeueAfterCount).Should(Equal(count))
	})

	It("corrects drift of a custom resource whose CRD is installed while the controller runs", func() {
		installCRD(gizmoCRD())
		g1 := gizmo("g1", 3)
		class := createClass(uniqueName("drift-gizmo"), g1)
		ns := createNamespace(class.Name)
		live := expectCreated(ns, g1)
		expectAnnotation(ns, g1)
		expectDriftWatchesReady(g1)

		By("changing a field the class sets")
		patchAsUser(live, func(obj *unstructured.Unstructured) {
			Expect(unstructured.SetNestedField(obj.Object, int64(9), specField, sizeField)).To(Succeed())
		})
		Eventually(liveSize(ns, g1)).Should(Equal(int64(3)))

		By("deleting it")
		Expect(k8sClient.Delete(ctx, live)).To(Succeed())
		Eventually(func(g Gomega) types.UID {
			return liveObject(ns, g1)(g).GetUID()
		}).ShouldNot(Equal(live.GetUID()))
		Expect(liveSize(ns, g1)(Default)).To(Equal(int64(3)))
	})

	It("writes nothing after convergence and reverts a write by another field manager once", func() {
		classData := map[string]string{colorKey: "yellow"}
		settings := configMap("settings", classData)
		objs := []*unstructured.Unstructured{
			settings,
			networkPolicy("ingress", "10.0.0.0/8", nil),
			roleBinding("readers", "view"),
			service("web"),
		}
		class := createClass(uniqueName("steady"), objs...)
		ns := createNamespace(class.Name)
		for _, obj := range objs {
			expectCreated(ns, obj)
		}
		expectAnnotation(ns, objs...)
		expectDriftWatchesReady(objs...)

		By("updating every object through the class")
		// Each update by the controller starts one more run through the drift watch. That run must
		// write nothing, or the controller would keep updating its own objects.
		labeled := make([]*unstructured.Unstructured, 0, len(objs))
		for _, obj := range objs {
			changed := obj.DeepCopy()
			changed.SetLabels(map[string]string{"tier": "web"})
			labeled = append(labeled, changed)
		}
		updateClass(class, labeled...)
		for _, obj := range objs {
			Eventually(liveLabels(ns, obj)).Should(HaveKeyWithValue("tier", "web"))
		}

		By("changing nothing for 10 seconds")
		converged := resourceVersions(ns, objs...)(Default)
		events := settledEvents(ns)
		Consistently(func(g Gomega) {
			g.Expect(resourceVersions(ns, objs...)(g)).To(Equal(converged))
			g.Expect(eventSeries(ns)(g)).To(Equal(events))
		}).WithTimeout(10 * time.Second).Should(Succeed())

		By("reverting a write by another field manager once")
		patchAsUser(liveObject(ns, settings)(Default), func(obj *unstructured.Unstructured) {
			Expect(unstructured.SetNestedField(obj.Object, "red", "data", colorKey)).To(Succeed())
		})
		Eventually(liveData(ns, settings)).Should(Equal(classData))
		newEvents := func(g Gomega) []eventsv1.Event {
			var found []eventsv1.Event
			for _, ev := range eventsAbout(ns)(g) {
				if _, seen := events[ev.Name]; !seen {
					found = append(found, ev)
				}
			}
			return found
		}
		// Each revert records a new event, because the event names the object's new resourceVersion.
		revertEvent := SatisfyAll(
			eventWith(corev1.EventTypeNormal, ReasonUpdated, "ConfigMap/settings"),
			HaveField("Series", BeNil()),
		)
		Eventually(newEvents).Should(ContainElement(revertEvent))
		reverted := resourceVersions(ns, objs...)(Default)
		Consistently(func(g Gomega) {
			g.Expect(resourceVersions(ns, objs...)(g)).To(Equal(reverted))
			g.Expect(newEvents(g)).To(ConsistOf(revertEvent))
		}).Should(Succeed())
		expected := maps.Clone(converged)
		expected["ConfigMap/settings"] = reverted["ConfigMap/settings"]
		Expect(reverted).To(Equal(expected), "only the edited object should change")
	})
})
