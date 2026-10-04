package resources

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
	"github.com/hydradon/namespaceclasscontroller/internal/inventory"
)

const (
	className       = "public-network"
	classUID        = types.UID("0c8e4a6e-2f6c-4a5b-9d0e-7a3f1c2b4d5e")
	targetNamespace = "web-portal"

	exampleGroup  = "example.com"
	widgetV1      = exampleGroup + "/v1"
	widgetV2      = exampleGroup + "/v2"
	rbacAPI       = "rbac.authorization.k8s.io/v1"
	networkingAPI = "networking.k8s.io/v1"

	kindConfigMap   = "ConfigMap"
	kindWidget      = "Widget"
	kindClusterRole = "ClusterRole"

	firstItemNotDecodable = "spec.resources[0]: cannot be decoded"

	ownerReferenceJSON = `{
		"apiVersion": "namespaceclass.akuity.io/v1alpha1",
		"kind": "NamespaceClass",
		"name": "public-network",
		"uid": "0c8e4a6e-2f6c-4a5b-9d0e-7a3f1c2b4d5e",
		"controller": true
	}`
)

// testMapper knows a few namespaced kinds, one cluster-scoped kind and two versions of Widget.
func testMapper() *meta.DefaultRESTMapper {
	mapper := meta.NewDefaultRESTMapper(nil)
	for _, gvk := range []schema.GroupVersionKind{
		{Version: "v1", Kind: kindConfigMap},
		{Version: "v1", Kind: "ServiceAccount"},
		{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"},
		{Group: exampleGroup, Version: "v1", Kind: kindWidget},
		{Group: exampleGroup, Version: "v2", Kind: kindWidget},
		{Group: exampleGroup, Version: "v1", Kind: kindConfigMap},
		{Group: exampleGroup, Version: "v1", Kind: "ServiceAccount"},
	} {
		mapper.Add(gvk, meta.RESTScopeNamespace)
	}
	mapper.Add(schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: kindClusterRole}, meta.RESTScopeRoot)
	return mapper
}

// newClass leaves TypeMeta empty, like a class read through a typed client.
func newClass(items ...string) *v1alpha1.NamespaceClass {
	class := &v1alpha1.NamespaceClass{ObjectMeta: metav1.ObjectMeta{Name: className, UID: classUID}}
	for _, item := range items {
		class.Spec.Resources = append(class.Spec.Resources, runtime.RawExtension{Raw: []byte(item)})
	}
	return class
}

// manifest returns a minimal JSON manifest with only the fields that identify the object.
func manifest(apiVersion, kind, name string) string {
	return fmt.Sprintf(`{"apiVersion":%q,"kind":%q,"metadata":{"name":%q}}`, apiVersion, kind, name)
}

func decodeJSON(t *testing.T, text string) *unstructured.Unstructured {
	t.Helper()
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON([]byte(text)); err != nil {
		t.Fatalf("decoding %s: %v", text, err)
	}
	return obj
}

func toJSON(obj *unstructured.Unstructured) string {
	data, err := obj.MarshalJSON()
	if err != nil {
		return fmt.Sprintf("<cannot encode: %v>", err)
	}
	return string(data)
}

func requireInvalidClass(t *testing.T, err error) *InvalidClassError {
	t.Helper()
	invalid, ok := errors.AsType[*InvalidClassError](err)
	if !ok {
		t.Fatalf("Build() error = %v (%T), want *InvalidClassError", err, err)
	}
	return invalid
}

func TestBuildRendersObjects(t *testing.T) {
	const widget = `{
		"apiVersion": "example.com/v1",
		"kind": "Widget",
		"metadata": {
			"name": "gadget",
			"uid": "item-uid",
			"resourceVersion": "42",
			"generation": 7,
			"creationTimestamp": "2020-01-01T00:00:00Z",
			"finalizers": ["example.com/keep"],
			"labels": {"team": "web"},
			"annotations": {"note": "hello"},
			"ownerReferences": [{"apiVersion": "v1", "kind": "ConfigMap", "name": "other", "uid": "other-uid"}],
			"managedFields": [{"manager": "kubectl", "operation": "Update"}]
		},
		"spec": {"size": 3, "tags": ["a", "b"], "nested": {"enabled": true}},
		"status": {"phase": "Ready"}
	}`
	const configMap = `{
		"apiVersion": "v1",
		"kind": "ConfigMap",
		"metadata": {"name": "settings"},
		"data": {"key": "value"},
		"binaryData": {"blob": "AAEC"}
	}`
	wantWidget := `{
		"apiVersion": "example.com/v1",
		"kind": "Widget",
		"metadata": {
			"name": "gadget",
			"namespace": "web-portal",
			"labels": {"namespaceclass.akuity.io/class": "public-network", "team": "web"},
			"annotations": {"note": "hello"},
			"ownerReferences": [` + ownerReferenceJSON + `]
		},
		"spec": {"size": 3, "tags": ["a", "b"], "nested": {"enabled": true}}
	}`
	wantConfigMap := `{
		"apiVersion": "v1",
		"kind": "ConfigMap",
		"metadata": {
			"name": "settings",
			"namespace": "web-portal",
			"labels": {"namespaceclass.akuity.io/class": "public-network"},
			"ownerReferences": [` + ownerReferenceJSON + `]
		},
		"data": {"key": "value"},
		"binaryData": {"blob": "AAEC"}
	}`

	class := newClass(widget, configMap)
	before := class.DeepCopy()

	got, err := Build(class, targetNamespace, testMapper())
	if err != nil {
		t.Fatalf("Build() returned an error: %v", err)
	}

	want := []*unstructured.Unstructured{decodeJSON(t, wantWidget), decodeJSON(t, wantConfigMap)}
	if len(got) != len(want) {
		t.Fatalf("Build() returned %d objects, want %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i].Object, want[i].Object) {
			t.Errorf("object %d =\n%s\nwant\n%s", i, toJSON(got[i]), toJSON(want[i]))
		}
	}

	owners := got[0].GetOwnerReferences()
	if len(owners) != 1 || owners[0].Controller == nil || !*owners[0].Controller {
		t.Errorf("ownerReferences = %+v, want exactly one with controller=true", owners)
	}
	if len(owners) == 1 && owners[0].BlockOwnerDeletion != nil {
		t.Errorf("blockOwnerDeletion = %v, want it unset", *owners[0].BlockOwnerDeletion)
	}

	if refs := inventory.RefsOf(got); refs.Len() != len(got) {
		t.Errorf("RefsOf(Build()) has %d refs for %d objects", refs.Len(), len(got))
	}
	if !reflect.DeepEqual(class, before) {
		t.Error("Build() changed the class")
	}
}

func TestBuildKeepsClassOrder(t *testing.T) {
	class := newClass(
		manifest("v1", kindConfigMap, "zeta"),
		manifest(widgetV1, kindWidget, "middle"),
		manifest("v1", kindConfigMap, "alpha"),
	)

	got, err := Build(class, targetNamespace, testMapper())
	if err != nil {
		t.Fatalf("Build() returned an error: %v", err)
	}

	names := make([]string, 0, len(got))
	for _, obj := range got {
		names = append(names, obj.GetKind()+"/"+obj.GetName())
	}
	want := []string{"ConfigMap/zeta", "Widget/middle", "ConfigMap/alpha"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Build() order = %v, want %v", names, want)
	}
}

func TestBuildOverridesReservedLabel(t *testing.T) {
	class := newClass(`{
		"apiVersion": "v1",
		"kind": "ConfigMap",
		"metadata": {"name": "settings", "labels": {"namespaceclass.akuity.io/class": "someone-else", "team": "web"}}
	}`)

	got, err := Build(class, targetNamespace, testMapper())
	if err != nil {
		t.Fatalf("Build() returned an error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Build() returned %d objects, want 1", len(got))
	}

	wantLabels := map[string]string{v1alpha1.ManagedByClassLabel: className, "team": "web"}
	if labels := got[0].GetLabels(); !reflect.DeepEqual(labels, wantLabels) {
		t.Errorf("labels = %v, want %v", labels, wantLabels)
	}
}

func TestBuildRejectsInvalidClass(t *testing.T) {
	tests := []struct {
		name  string
		items []string
		want  []string // one substring for each expected problem, in order
	}{
		{
			name:  "metadata.namespace is set",
			items: []string{`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"settings","namespace":"other"}}`},
			want:  []string{`spec.resources[0] (ConfigMap/settings): metadata.namespace must be empty`},
		},
		{
			name:  "same group, kind and name in two versions",
			items: []string{manifest(widgetV1, kindWidget, "gadget"), manifest(widgetV2, kindWidget, "gadget")},
			want:  []string{`spec.resources[1] (Widget/gadget): duplicates spec.resources[0]`},
		},
		{
			name:  "cluster-scoped kind",
			items: []string{manifest(rbacAPI, kindClusterRole, "admin")},
			want:  []string{`spec.resources[0] (ClusterRole/admin): kind ClusterRole is cluster-scoped`},
		},
		{
			name:  "apiVersion with two slashes",
			items: []string{manifest("a/b/c", kindWidget, "gadget")},
			want:  []string{`spec.resources[0]: cannot be decoded: apiVersion "a/b/c" is not valid`},
		},
		{
			name:  "apiVersion without a version",
			items: []string{manifest("example.com/", kindWidget, "gadget")},
			want:  []string{`spec.resources[0] (Widget/gadget): apiVersion "example.com/" is not valid`},
		},
		{
			name:  "apiVersion is missing",
			items: []string{`{"kind":"Widget","metadata":{"name":"gadget"}}`},
			want:  []string{`spec.resources[0] (Widget/gadget): apiVersion "" is not valid`},
		},
		{
			name:  "ServiceAccount default",
			items: []string{manifest("v1", "ServiceAccount", "default")},
			want:  []string{`spec.resources[0] (ServiceAccount/default): ServiceAccount/default is created by Kubernetes in every namespace`},
		},
		{
			name:  "ConfigMap kube-root-ca.crt",
			items: []string{manifest("v1", kindConfigMap, "kube-root-ca.crt")},
			want:  []string{`spec.resources[0] (ConfigMap/kube-root-ca.crt): ConfigMap/kube-root-ca.crt is created by Kubernetes in every namespace`},
		},
		{
			name:  "name is empty",
			items: []string{`{"apiVersion":"v1","kind":"ConfigMap","metadata":{}}`},
			want:  []string{`spec.resources[0] (ConfigMap): metadata.name is required`},
		},
		{
			name:  "generateName is set",
			items: []string{`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"settings","generateName":"settings-"}}`},
			want:  []string{`spec.resources[0] (ConfigMap/settings): metadata.generateName is not supported`},
		},
		{
			name:  "item without a kind cannot be decoded",
			items: []string{`{"apiVersion":"v1","metadata":{"name":"settings"}}`},
			want:  []string{firstItemNotDecodable},
		},
		{
			name:  "item that is not an object cannot be decoded",
			items: []string{`[1, 2]`},
			want:  []string{firstItemNotDecodable},
		},
		{
			name:  "item in an unknown encoding cannot be decoded",
			items: []string{"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings"},
			want:  []string{firstItemNotDecodable},
		},
		{
			name:  "null item cannot be decoded",
			items: []string{manifest("v1", kindConfigMap, "settings"), `null`},
			want:  []string{`spec.resources[1]: cannot be decoded`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Build(newClass(tt.items...), targetNamespace, testMapper())
			invalid := requireInvalidClass(t, err)
			if got != nil {
				t.Errorf("Build() returned %d objects together with an error, want none", len(got))
			}
			if len(invalid.Problems) != len(tt.want) {
				t.Fatalf("problems = %q, want %d problem(s) like %q", invalid.Problems, len(tt.want), tt.want)
			}
			for i, want := range tt.want {
				if !strings.Contains(invalid.Problems[i], want) {
					t.Errorf("problem %d = %q, want it to contain %q", i, invalid.Problems[i], want)
				}
			}
		})
	}
}

func TestBuildReportsEveryProblem(t *testing.T) {
	class := newClass(
		/* 0 */ `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"settings","namespace":"other"}}`,
		/* 1 */ manifest(networkingAPI, "NetworkPolicy", "ingress"),
		/* 2 */ manifest(rbacAPI, kindClusterRole, "admin"),
		/* 3 */ manifest("v1", "ServiceAccount", "default"),
		/* 4 */ manifest(widgetV1, kindWidget, "gadget"),
		/* 5 */ manifest(widgetV2, kindWidget, "gadget"),
		/* 6 */ manifest("a/b/c", kindWidget, "other"),
		/* 7 */ `{"apiVersion":"v1","metadata":{"name":"no-kind"}}`,
		/* 8 */ manifest(widgetV1, "Unknown", "mystery"),
	)

	got, err := Build(class, targetNamespace, testMapper())
	invalid := requireInvalidClass(t, err)
	if got != nil {
		t.Errorf("Build() returned %d objects together with an error, want none", len(got))
	}
	if meta.IsNoMatchError(err) {
		t.Error("an unknown kind must not hide the structural problems: IsNoMatchError() = true")
	}

	want := []string{
		"spec.resources[0] (ConfigMap/settings): metadata.namespace must be empty",
		"spec.resources[2] (ClusterRole/admin): kind ClusterRole is cluster-scoped",
		"spec.resources[3] (ServiceAccount/default): ServiceAccount/default is created by Kubernetes",
		"spec.resources[5] (Widget/gadget): duplicates spec.resources[4]",
		`spec.resources[6]: cannot be decoded: apiVersion "a/b/c" is not valid`,
		"spec.resources[7]: cannot be decoded",
	}
	if len(invalid.Problems) != len(want) {
		t.Fatalf("problems =\n%s\nwant %d problems", strings.Join(invalid.Problems, "\n"), len(want))
	}
	for i := range want {
		if !strings.Contains(invalid.Problems[i], want[i]) {
			t.Errorf("problem %d = %q, want it to contain %q", i, invalid.Problems[i], want[i])
		}
	}
	if message, wantMessage := err.Error(), strings.Join(invalid.Problems, "; "); message != wantMessage {
		t.Errorf("Error() = %q, want the problems joined with \"; \": %q", message, wantMessage)
	}
}

func TestBuildReportsProblemsOfOneItemTogether(t *testing.T) {
	class := newClass(`{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"ClusterRole","metadata":{"name":"admin","namespace":"other"}}`)

	_, err := Build(class, targetNamespace, testMapper())
	invalid := requireInvalidClass(t, err)
	if len(invalid.Problems) != 2 {
		t.Fatalf("problems = %q, want 2 (namespace set and cluster-scoped)", invalid.Problems)
	}
	if !strings.Contains(invalid.Problems[0], "metadata.namespace must be empty") ||
		!strings.Contains(invalid.Problems[1], "is cluster-scoped") {
		t.Errorf("problems = %q", invalid.Problems)
	}
}

func TestBuildAllowsDeniedNamesOutsideTheCoreGroup(t *testing.T) {
	class := newClass(
		manifest(widgetV1, "ServiceAccount", "default"),
		manifest(widgetV1, kindConfigMap, "kube-root-ca.crt"),
		manifest("v1", "ServiceAccount", "builder"),
		manifest("v1", kindConfigMap, "settings"),
	)

	got, err := Build(class, targetNamespace, testMapper())
	if err != nil {
		t.Fatalf("Build() returned an error: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("Build() returned %d objects, want 4", len(got))
	}
}

func TestBuildUnknownKindIsNoMatch(t *testing.T) {
	tests := []struct {
		name  string
		items []string
		want  string
	}{
		{
			name:  "kind that is not installed",
			items: []string{manifest("v1", kindConfigMap, "settings"), manifest(widgetV1, "Gadget", "one")},
			want:  `spec.resources[1] (Gadget/one)`,
		},
		{
			name:  "kind that exists only in another version",
			items: []string{manifest("example.com/v3", kindWidget, "gadget")},
			want:  `spec.resources[0] (Widget/gadget)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Build(newClass(tt.items...), targetNamespace, testMapper())
			if err == nil {
				t.Fatal("Build() returned no error for an unknown kind")
			}
			if !meta.IsNoMatchError(err) {
				t.Errorf("IsNoMatchError(%v) = false, want true", err)
			}
			if _, ok := errors.AsType[*InvalidClassError](err); ok {
				t.Errorf("an unknown kind must not be an InvalidClassError: %v", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to name the item: %q", err.Error(), tt.want)
			}
			if got != nil {
				t.Errorf("Build() returned %d objects together with an error, want none", len(got))
			}
		})
	}
}

func TestBuildReportsEveryUnknownKind(t *testing.T) {
	class := newClass(manifest(widgetV1, "Gadget", "one"), manifest(widgetV1, "Gizmo", "two"))

	_, err := Build(class, targetNamespace, testMapper())
	if !meta.IsNoMatchError(err) {
		t.Fatalf("IsNoMatchError(%v) = false, want true", err)
	}
	for _, kind := range []string{"Gadget", "Gizmo"} {
		if !strings.Contains(err.Error(), kind) {
			t.Errorf("error = %q, want it to mention %s", err.Error(), kind)
		}
	}
}

func TestBuildStructuralProblemsWinOverUnknownKinds(t *testing.T) {
	class := newClass(manifest(widgetV1, "Gadget", "one"), manifest("v1", "ServiceAccount", "default"))

	_, err := Build(class, targetNamespace, testMapper())
	invalid := requireInvalidClass(t, err)
	if len(invalid.Problems) != 1 {
		t.Errorf("problems = %q, want only the ServiceAccount problem", invalid.Problems)
	}
	if meta.IsNoMatchError(err) {
		t.Error("IsNoMatchError() = true, want false when the class also has structural problems")
	}
}

type failingMapper struct {
	meta.RESTMapper
	err error
}

func (m failingMapper) RESTMapping(schema.GroupKind, ...string) (*meta.RESTMapping, error) {
	return nil, m.err
}

func TestBuildReturnsOtherMapperErrorsWrapped(t *testing.T) {
	errDiscovery := errors.New("discovery is unavailable")
	class := newClass(manifest("v1", kindConfigMap, "settings"))

	got, err := Build(class, targetNamespace, failingMapper{err: errDiscovery})
	if !errors.Is(err, errDiscovery) {
		t.Fatalf("Build() error = %v, want it to wrap %v", err, errDiscovery)
	}
	if _, ok := errors.AsType[*InvalidClassError](err); ok {
		t.Error("a mapper failure must not be an InvalidClassError")
	}
	if meta.IsNoMatchError(err) {
		t.Error("IsNoMatchError() = true for a mapper failure that is not a no-match error")
	}
	if !strings.Contains(err.Error(), "spec.resources[0] (ConfigMap/settings)") {
		t.Errorf("error = %q, want it to name the item", err.Error())
	}
	if got != nil {
		t.Errorf("Build() returned %d objects together with an error, want none", len(got))
	}
}

func TestBuildPreservesInt64(t *testing.T) {
	const big = int64(9007199254740993) // 2^53 + 1 is not exact as a float64
	class := newClass(`{
		"apiVersion": "example.com/v1",
		"kind": "Widget",
		"metadata": {"name": "gadget"},
		"spec": {"limit": 9007199254740993}
	}`)

	got, err := Build(class, targetNamespace, testMapper())
	if err != nil {
		t.Fatalf("Build() returned an error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Build() returned %d objects, want 1", len(got))
	}

	limit, found, err := unstructured.NestedInt64(got[0].Object, "spec", "limit")
	if err != nil || !found || limit != big {
		t.Errorf("spec.limit = %d (found %v, error %v), want %d", limit, found, err, big)
	}
	if text := toJSON(got[0]); !strings.Contains(text, "9007199254740993") {
		t.Errorf("encoded object = %s, want it to contain 9007199254740993", text)
	}
}

func TestBuildDecodesItemsStoredAsObjects(t *testing.T) {
	class := &v1alpha1.NamespaceClass{ObjectMeta: metav1.ObjectMeta{Name: className, UID: classUID}}
	class.Spec.Resources = []runtime.RawExtension{{Object: decodeJSON(t, manifest(widgetV1, kindWidget, "gadget"))}}

	got, err := Build(class, targetNamespace, testMapper())
	if err != nil {
		t.Fatalf("Build() returned an error: %v", err)
	}
	if len(got) != 1 || got[0].GetName() != "gadget" || got[0].GetNamespace() != targetNamespace {
		t.Errorf("Build() = %v, want one Widget named gadget in %s", got, targetNamespace)
	}
}

func TestBuildEmptyClass(t *testing.T) {
	for name, class := range map[string]*v1alpha1.NamespaceClass{
		"no resources field": newClass(),
		"empty list": {
			ObjectMeta: metav1.ObjectMeta{Name: className, UID: classUID},
			Spec:       v1alpha1.NamespaceClassSpec{Resources: []runtime.RawExtension{}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Build(class, targetNamespace, testMapper())
			if err != nil {
				t.Fatalf("Build() returned an error: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("Build() returned %d objects, want none", len(got))
			}
		})
	}
}

func TestInvalidClassErrorJoinsProblems(t *testing.T) {
	err := &InvalidClassError{Problems: []string{"first problem", "second problem"}}
	if got, want := err.Error(), "first problem; second problem"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
