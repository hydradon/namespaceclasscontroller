package inventory

import (
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
)

const (
	networkPolicyGroup = "networking.k8s.io"
	kindConfigMap      = "ConfigMap"
	kindNetworkPolicy  = "NetworkPolicy"
	kindClass          = "NamespaceClass"
	className          = "public-network"
)

var (
	classAPIVersion = v1alpha1.GroupVersion.String()

	networkInfo   = Ref{Kind: kindConfigMap, Name: "network-info"}
	ingressPolicy = Ref{Group: networkPolicyGroup, Kind: kindNetworkPolicy, Name: "ingress"}
	egressPolicy  = Ref{Group: networkPolicyGroup, Kind: kindNetworkPolicy, Name: "egress"}
)

func TestRefStringAndGroupKind(t *testing.T) {
	tests := []struct {
		ref       Ref
		wantText  string
		wantGroup schema.GroupKind
	}{
		{
			ref:       ingressPolicy,
			wantText:  "NetworkPolicy.networking.k8s.io/ingress",
			wantGroup: schema.GroupKind{Group: networkPolicyGroup, Kind: "NetworkPolicy"},
		},
		{
			ref:       networkInfo,
			wantText:  "ConfigMap/network-info",
			wantGroup: schema.GroupKind{Kind: "ConfigMap"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.wantText, func(t *testing.T) {
			if got := tt.ref.String(); got != tt.wantText {
				t.Errorf("String() = %q, want %q", got, tt.wantText)
			}
			if got := tt.ref.GroupKind(); got != tt.wantGroup {
				t.Errorf("GroupKind() = %v, want %v", got, tt.wantGroup)
			}
		})
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    Set
		wantErr string
	}{
		{name: "empty value is an empty set", value: "", want: sets.New[Ref]()},
		{name: "empty list is an empty set", value: "[]", want: sets.New[Ref]()},
		{
			name:  "valid list",
			value: `[{"kind":"ConfigMap","name":"network-info"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]`,
			want:  sets.New(networkInfo, ingressPolicy),
		},
		{
			name:  "repeated entries count once",
			value: `[{"kind":"ConfigMap","name":"network-info"},{"kind":"ConfigMap","name":"network-info"}]`,
			want:  sets.New(networkInfo),
		},
		{name: "malformed JSON", value: `[{"kind":"ConfigMap"`, wantErr: "expected a JSON list"},
		{name: "not a list", value: `{"kind":"ConfigMap","name":"network-info"}`, wantErr: "expected a JSON list"},
		{name: "missing kind", value: `[{"kind":"ConfigMap","name":"a"},{"name":"b"}]`, wantErr: "entry 1 has no kind"},
		{name: "missing name", value: `[{"kind":"ConfigMap"}]`, wantErr: "entry 0 has no name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.value)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Parse(%q) error = %v, want an error containing %q", tt.value, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) returned an error: %v", tt.value, err)
			}
			if got == nil {
				t.Fatalf("Parse(%q) returned a nil set; callers insert into the result", tt.value)
			}
			if !got.Equal(tt.want) {
				t.Errorf("Parse(%q) = %v, want %v", tt.value, names(got), names(tt.want))
			}
		})
	}
}

func TestEncodeSortedAndRoundTrip(t *testing.T) {
	t.Run("sorted by group, then kind, then name", func(t *testing.T) {
		set := sets.New(
			ingressPolicy,
			Ref{Kind: kindConfigMap, Name: "settings"},
			egressPolicy,
			networkInfo,
			Ref{Group: "b.example.com", Kind: "Alpha", Name: "one"},
			Ref{Group: "a.example.com", Kind: "Zeta", Name: "one"},
		)
		// The core group is empty, so it comes first.
		want := `[` +
			`{"kind":"ConfigMap","name":"network-info"},` +
			`{"kind":"ConfigMap","name":"settings"},` +
			`{"group":"a.example.com","kind":"Zeta","name":"one"},` +
			`{"group":"b.example.com","kind":"Alpha","name":"one"},` +
			`{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"egress"},` +
			`{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}` +
			`]`

		got := Encode(set)
		if got != want {
			t.Fatalf("Encode() =\n%s\nwant\n%s", got, want)
		}

		parsed, err := Parse(got)
		if err != nil {
			t.Fatalf("Parse(Encode()) returned an error: %v", err)
		}
		if !parsed.Equal(set) {
			t.Errorf("Parse(Encode(set)) = %v, want %v", names(parsed), names(set))
		}
		if again := Encode(parsed); again != got {
			t.Errorf("Encode(Parse(Encode(set))) = %q, want %q", again, got)
		}
	})

	t.Run("empty set gives an empty string", func(t *testing.T) {
		if got := Encode(sets.New[Ref]()); got != "" {
			t.Errorf("Encode(empty set) = %q, want an empty string", got)
		}
		if got := Encode(nil); got != "" {
			t.Errorf("Encode(nil) = %q, want an empty string", got)
		}
	})

	t.Run("core group is omitted", func(t *testing.T) {
		if got := Encode(sets.New(networkInfo)); strings.Contains(got, "group") {
			t.Errorf("Encode() = %q, want no group key for the core group", got)
		}
	})
}

// The reconciler combines the list in the annotation with the desired list using these operations.
func TestSetOperations(t *testing.T) {
	previous := sets.New(networkInfo, ingressPolicy)
	desired := sets.New(ingressPolicy, egressPolicy)

	if got, want := previous.Union(desired), sets.New(networkInfo, ingressPolicy, egressPolicy); !got.Equal(want) {
		t.Errorf("previous union desired = %v, want %v", names(got), names(want))
	}
	if got, want := previous.Difference(desired), sets.New(networkInfo); !got.Equal(want) {
		t.Errorf("previous minus desired = %v, want %v", names(got), names(want))
	}
	if got, want := desired.Difference(previous), sets.New(egressPolicy); !got.Equal(want) {
		t.Errorf("desired minus previous = %v, want %v", names(got), names(want))
	}

	sameElements := sets.New(egressPolicy, ingressPolicy)
	if !desired.Equal(sameElements) {
		t.Errorf("%v and %v hold the same refs and must be equal", names(desired), names(sameElements))
	}
	if Encode(desired) != Encode(sameElements) {
		t.Errorf("equal sets must encode to the same text, got %q and %q", Encode(desired), Encode(sameElements))
	}

	sameNameOtherGroup := Ref{Kind: kindNetworkPolicy, Name: "ingress"}
	if desired.Has(sameNameOtherGroup) {
		t.Errorf("%v must not match %v: the group is part of the identity", sameNameOtherGroup, ingressPolicy)
	}
}

func TestRefsOf(t *testing.T) {
	objs := []*unstructured.Unstructured{
		newObject("v1", kindConfigMap, "network-info"),
		newObject("networking.k8s.io/v1", kindNetworkPolicy, "ingress"),
		newObject("networking.k8s.io/v1beta1", kindNetworkPolicy, "ingress"),
		newObject("networking.k8s.io/v1", kindNetworkPolicy, "egress"),
	}

	got := RefsOf(objs)
	want := sets.New(networkInfo, ingressPolicy, egressPolicy)
	if !got.Equal(want) {
		t.Errorf("RefsOf() = %v, want %v (the version is not part of the identity)", names(got), names(want))
	}

	empty := RefsOf(nil)
	if empty == nil || empty.Len() != 0 {
		t.Errorf("RefsOf(nil) = %v, want an empty, non-nil set", names(empty))
	}
}

func TestIsManaged(t *testing.T) {
	classOwner := metav1.OwnerReference{APIVersion: classAPIVersion, Kind: kindClass, Name: className, UID: "uid-1"}
	otherVersion := classOwner
	otherVersion.APIVersion = v1alpha1.GroupVersion.Group + "/v1beta1"
	otherGroup := classOwner
	otherGroup.APIVersion = "example.com/v1"
	otherKind := classOwner
	otherKind.Kind = "Something"
	deployment := metav1.OwnerReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "uid-2"}
	badAPIVersion := classOwner
	badAPIVersion.APIVersion = "a/b/c"

	tests := []struct {
		name   string
		labels map[string]string
		owners []metav1.OwnerReference
		want   bool
	}{
		{name: "class label with a value", labels: map[string]string{v1alpha1.ManagedByClassLabel: className}, want: true},
		{name: "class label with an empty value", labels: map[string]string{v1alpha1.ManagedByClassLabel: ""}, want: true},
		{name: "owner reference to a NamespaceClass only", owners: []metav1.OwnerReference{classOwner}, want: true},
		{name: "owner reference to another version of the API", owners: []metav1.OwnerReference{otherVersion}, want: true},
		{name: "NamespaceClass owner among other owners", owners: []metav1.OwnerReference{deployment, classOwner}, want: true},
		{name: "no labels and no owners", want: false},
		{name: "unrelated labels", labels: map[string]string{"app": "web", "namespaceclass.akuity.io/name": "public-network"}, want: false},
		{name: "owner reference to another kind", owners: []metav1.OwnerReference{deployment}, want: false},
		{name: "kind NamespaceClass in another group", owners: []metav1.OwnerReference{otherGroup}, want: false},
		{name: "other kind in the same group", owners: []metav1.OwnerReference{otherKind}, want: false},
		{name: "owner reference with an invalid apiVersion", owners: []metav1.OwnerReference{badAPIVersion}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &metav1.ObjectMeta{Labels: tt.labels, OwnerReferences: tt.owners}
			if got := IsManaged(obj); got != tt.want {
				t.Errorf("IsManaged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsManagedWorksForUnstructuredObjects(t *testing.T) {
	labeled := newObject("v1", kindConfigMap, "network-info")
	labeled.SetLabels(map[string]string{v1alpha1.ManagedByClassLabel: className})
	if !IsManaged(labeled) {
		t.Error("IsManaged(object with the class label) = false, want true")
	}

	owned := newObject("v1", kindConfigMap, "network-info")
	owned.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: classAPIVersion, Kind: "NamespaceClass", Name: "x", UID: "uid-1"}})
	if !IsManaged(owned) {
		t.Error("IsManaged(object owned by a NamespaceClass) = false, want true")
	}

	if IsManaged(newObject("v1", kindConfigMap, "network-info")) {
		t.Error("IsManaged(object without label or owner) = true, want false")
	}
}

func newObject(apiVersion, kind, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(apiVersion)
	obj.SetKind(kind)
	obj.SetName(name)
	return obj
}

// names lists the members of the set as sorted text, which reads better in failure messages.
func names(set Set) []string {
	out := make([]string, 0, set.Len())
	for ref := range set {
		out = append(out, ref.String())
	}
	slices.Sort(out)
	return out
}
