// Package inventory keeps the list of objects the controller created in a Namespace
// and tells which objects belong to the controller.
package inventory

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
)

// Ref identifies an object in a namespace by group, kind and name. The version is left out on
// purpose: the same object written with two API versions is still one object.
type Ref struct {
	Group string `json:"group,omitempty"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
}

type Set = sets.Set[Ref]

func (r Ref) GroupKind() schema.GroupKind {
	return schema.GroupKind{Group: r.Group, Kind: r.Kind}
}

// String returns Kind.group/name, or Kind/name for the core group.
func (r Ref) String() string {
	if r.Group == "" {
		return r.Kind + "/" + r.Name
	}
	return r.Kind + "." + r.Group + "/" + r.Name
}

// RefOf returns the group, kind and name of obj.
func RefOf(obj *unstructured.Unstructured) Ref {
	return Ref{Group: obj.GroupVersionKind().Group, Kind: obj.GetKind(), Name: obj.GetName()}
}

func RefsOf(objs []*unstructured.Unstructured) Set {
	refs := make(Set, len(objs))
	for _, obj := range objs {
		refs.Insert(RefOf(obj))
	}
	return refs
}

// Sorted returns the refs of s ordered by group, kind and name.
func Sorted(s Set) []Ref {
	refs := s.UnsortedList()
	slices.SortFunc(refs, func(a, b Ref) int {
		return cmp.Or(cmp.Compare(a.Group, b.Group), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
	})
	return refs
}

// Parse reads the value of the managed-resources annotation. An empty value is an empty set.
func Parse(value string) (Set, error) {
	if value == "" {
		return sets.New[Ref](), nil
	}
	var refs []Ref
	if err := json.Unmarshal([]byte(value), &refs); err != nil {
		return nil, fmt.Errorf("expected a JSON list of {group, kind, name}: %w", err)
	}
	set := make(Set, len(refs))
	for i, ref := range refs {
		switch {
		case ref.Kind == "":
			return nil, fmt.Errorf("entry %d has no kind", i)
		case ref.Name == "":
			return nil, fmt.Errorf("entry %d has no name", i)
		}
		set.Insert(ref)
	}
	return set, nil
}

// Encode returns the value for the managed-resources annotation: a JSON list sorted by group,
// kind and name, so equal sets always give equal text. An empty set gives an empty string.
func Encode(s Set) string {
	if s.Len() == 0 {
		return ""
	}
	// Encoding a list of plain strings cannot fail.
	encoded, _ := json.Marshal(Sorted(s))
	return string(encoded)
}

// IsManaged reports whether the object belongs to the controller: it has the class label (with
// any value) or an owner reference to a NamespaceClass. Either one is enough, so the controller
// still treats the object as its own when only the label was removed.
func IsManaged(obj metav1.Object) bool {
	if _, ok := obj.GetLabels()[v1alpha1.ManagedByClassLabel]; ok {
		return true
	}
	return slices.ContainsFunc(obj.GetOwnerReferences(), ownedByClass)
}

func ownedByClass(ref metav1.OwnerReference) bool {
	if ref.Kind != v1alpha1.NamespaceClassKind {
		return false
	}
	gv, err := schema.ParseGroupVersion(ref.APIVersion)
	return err == nil && gv.Group == v1alpha1.GroupVersion.Group
}
