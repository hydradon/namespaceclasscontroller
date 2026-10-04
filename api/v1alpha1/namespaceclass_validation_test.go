package v1alpha1_test

import (
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
)

var classCounter atomic.Int64

// newClass returns a class holding the given items, each written as a JSON manifest.
// An empty name picks a unique one.
func newClass(name string, items ...string) *v1alpha1.NamespaceClass {
	if name == "" {
		name = fmt.Sprintf("test-class-%d", classCounter.Add(1))
	}
	class := &v1alpha1.NamespaceClass{ObjectMeta: metav1.ObjectMeta{Name: name}}
	for _, item := range items {
		class.Spec.Resources = append(class.Spec.Resources, runtime.RawExtension{Raw: []byte(item)})
	}
	return class
}

func configMapItems(count int) []string {
	items := make([]string, count)
	for i := range items {
		items[i] = fmt.Sprintf(`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "settings-%d"}}`, i)
	}
	return items
}

func TestNamespaceClassValidation(t *testing.T) {
	const networkPolicy = `{
		"apiVersion": "networking.k8s.io/v1",
		"kind": "NetworkPolicy",
		"metadata": {"name": "allow-ingress-from-internet"},
		"spec": {
			"podSelector": {},
			"policyTypes": ["Ingress"],
			"ingress": [{"from": [{"ipBlock": {"cidr": "0.0.0.0/0"}}]}]
		}
	}`
	const widget = `{
		"apiVersion": "example.com/v1",
		"kind": "Widget",
		"metadata": {"name": "gadget"},
		"spec": {"size": 3}
	}`

	tests := []struct {
		name      string
		className string // empty picks a unique name
		items     []string
		wantErr   string // empty means the class must be accepted
	}{
		{name: "NetworkPolicy item is accepted", items: []string{networkPolicy}},
		{name: "unknown kind is accepted", items: []string{widget}},
		{name: "empty resources is accepted"},
		{name: "100 items are accepted", items: configMapItems(100)},
		{name: "class name of 63 characters is accepted", className: strings.Repeat("a", 63)},
		{
			name:    "item without metadata is rejected",
			items:   []string{`{"apiVersion": "v1", "kind": "ConfigMap"}`},
			wantErr: "metadata.name is required",
		},
		{
			name:    "item with empty name is rejected",
			items:   []string{`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": ""}}`},
			wantErr: "metadata.name is required",
		},
		{
			name: "item with generateName is rejected",
			items: []string{
				`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "settings", "generateName": "settings-"}}`,
			},
			wantErr: "metadata.generateName is not supported; set metadata.name",
		},
		{
			name:    "item without kind is rejected",
			items:   []string{`{"apiVersion": "v1", "metadata": {"name": "settings"}}`},
			wantErr: "spec.resources[0].kind: Required value",
		},
		{
			name:    "item without apiVersion is rejected",
			items:   []string{`{"kind": "ConfigMap", "metadata": {"name": "settings"}}`},
			wantErr: "spec.resources[0].apiVersion: Required value",
		},
		{
			name:    "101 items are rejected",
			items:   configMapItems(101),
			wantErr: "must have at most 100 items",
		},
		{
			name:      "class name of 64 characters is rejected",
			className: strings.Repeat("a", 64),
			wantErr:   "metadata.name must be at most 63 characters",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := createClass(t, newClass(tc.className, tc.items...))

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected the class to be accepted, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, but the class was accepted", tc.wantErr)
			}
			if !apierrors.IsInvalid(err) {
				t.Errorf("expected an Invalid error, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected an error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestItemFieldsKeptAndUnknownMetadataDropped(t *testing.T) {
	items := []string{
		`{
			"apiVersion": "networking.k8s.io/v1",
			"kind": "NetworkPolicy",
			"metadata": {"name": "allow-web", "labels": {"team": "web"}, "foo": "bar"},
			"spec": {
				"podSelector": {"matchLabels": {"app": "web"}},
				"policyTypes": ["Ingress"],
				"ingress": [{"from": [{"ipBlock": {"cidr": "10.0.0.0/8"}}], "ports": [{"port": 8080, "protocol": "TCP"}]}]
			}
		}`,
		`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "settings"}, "data": {"mode": "strict"}}`,
		`{
			"apiVersion": "example.com/v1",
			"kind": "Widget",
			"metadata": {"name": "gadget"},
			"spec": {"size": 3, "ratio": 0.5, "enabled": true, "tags": ["a", "b"], "nested": {"list": [1, 2]}}
		}`,
	}
	class := newClass("", items...)
	if err := createClass(t, class); err != nil {
		t.Fatalf("creating the class: %v", err)
	}

	var stored v1alpha1.NamespaceClass
	if err := k8sClient.Get(t.Context(), client.ObjectKeyFromObject(class), &stored); err != nil {
		t.Fatalf("reading the class back: %v", err)
	}
	if len(stored.Spec.Resources) != len(items) {
		t.Fatalf("expected %d items, got %d", len(items), len(stored.Spec.Resources))
	}

	for i, original := range items {
		want := decodeItem(t, runtime.RawExtension{Raw: []byte(original)})
		unstructured.RemoveNestedField(want.Object, "metadata", "foo")
		got := decodeItem(t, stored.Spec.Resources[i])

		if !reflect.DeepEqual(got.Object, want.Object) {
			t.Errorf("item %d changed\n got: %v\nwant: %v", i, got.Object, want.Object)
		}
	}
}

// decodeItem reads an item with RawExtension.MarshalJSON and Unstructured.UnmarshalJSON.
func decodeItem(t *testing.T, item runtime.RawExtension) *unstructured.Unstructured {
	t.Helper()
	data, err := item.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling the item: %v", err)
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(data); err != nil {
		t.Fatalf("decoding the item %s: %v", data, err)
	}
	return obj
}
