package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
)

func testNamespace(resourceVersion string, labels, annotations map[string]string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:            "ns",
		ResourceVersion: resourceVersion,
		Labels:          labels,
		Annotations:     annotations,
	}}
}

func TestNamespacePredicate(t *testing.T) {
	classLabel := func(class string) map[string]string {
		return map[string]string{v1alpha1.ClassLabel: class}
	}
	otherLabel := map[string]string{"team": "a"}
	recorded := map[string]string{v1alpha1.ManagedResourcesAnnotation: `[{"kind":"ConfigMap","name":"a"}]`}

	createTests := []struct {
		name string
		ns   *corev1.Namespace
		want bool
	}{
		{"no label and no annotation", testNamespace("1", otherLabel, nil), false},
		{"class label", testNamespace("1", classLabel("a"), nil), true},
		{"empty class label", testNamespace("1", classLabel(""), nil), true},
		{"annotation only (label removed while the controller was stopped)", testNamespace("1", nil, recorded), true},
	}
	for _, tc := range createTests {
		t.Run("create/"+tc.name, func(t *testing.T) {
			if got := namespacePredicate().Create(event.CreateEvent{Object: tc.ns}); got != tc.want {
				t.Errorf("Create() = %v, want %v", got, tc.want)
			}
		})
	}

	updateTests := []struct {
		name     string
		old, new *corev1.Namespace
		want     bool
	}{
		{"label added", testNamespace("1", nil, nil), testNamespace("2", classLabel("a"), nil), true},
		{"label removed", testNamespace("1", classLabel("a"), recorded), testNamespace("2", nil, recorded), true},
		{"label switched", testNamespace("1", classLabel("a"), nil), testNamespace("2", classLabel("b"), nil), true},
		{"label set to an empty value", testNamespace("1", classLabel("a"), nil), testNamespace("2", classLabel(""), nil), true},
		{"annotation written, same label", testNamespace("1", classLabel("a"), nil), testNamespace("2", classLabel("a"), recorded), false},
		{"other label changed", testNamespace("1", classLabel("a"), nil), testNamespace("2", map[string]string{v1alpha1.ClassLabel: "a", "team": "b"}, nil), false},
		{"other namespace changed", testNamespace("1", otherLabel, nil), testNamespace("2", nil, nil), false},
		{"resync with label", testNamespace("1", classLabel("a"), nil), testNamespace("1", classLabel("a"), nil), true},
		{"resync with annotation only", testNamespace("1", nil, recorded), testNamespace("1", nil, recorded), true},
		{"resync of other namespace", testNamespace("1", otherLabel, nil), testNamespace("1", otherLabel, nil), false},
	}
	for _, tc := range updateTests {
		t.Run("update/"+tc.name, func(t *testing.T) {
			got := namespacePredicate().Update(event.UpdateEvent{ObjectOld: tc.old, ObjectNew: tc.new})
			if got != tc.want {
				t.Errorf("Update() = %v, want %v", got, tc.want)
			}
		})
	}

	labeled := testNamespace("1", classLabel("a"), recorded)
	if namespacePredicate().Delete(event.DeleteEvent{Object: labeled}) {
		t.Error("Delete() = true, want false: Kubernetes deletes the contents of a deleted namespace")
	}
	if namespacePredicate().Generic(event.GenericEvent{Object: labeled}) {
		t.Error("Generic() = true, want false")
	}
}
