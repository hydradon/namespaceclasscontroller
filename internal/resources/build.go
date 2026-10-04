// Package resources turns a NamespaceClass into the objects to apply in one Namespace.
package resources

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
	"github.com/hydradon/namespaceclasscontroller/internal/inventory"
)

// createdByKubernetes holds the objects Kubernetes creates in every namespace. A class must not
// manage them, because Kubernetes creates and changes them itself.
var createdByKubernetes = sets.New(
	inventory.Ref{Kind: "ServiceAccount", Name: "default"},
	inventory.Ref{Kind: "ConfigMap", Name: "kube-root-ca.crt"},
)

// InvalidClassError lists everything that is wrong with a class. Only editing the class fixes
// it, so callers should not retry.
type InvalidClassError struct {
	Problems []string
}

func (e *InvalidClassError) Error() string {
	return strings.Join(e.Problems, "; ")
}

// Build returns the objects the class creates in the namespace, in the order of the class's
// items. It checks the whole class first.
//
// Problems in the class are returned together as an *InvalidClassError. A kind that the mapper
// does not know is not a problem in the class, because its CRD may not be installed yet. It is
// returned as a wrapped mapper error, so meta.IsNoMatchError(err) is true. When the class has
// problems and also an unknown kind, the *InvalidClassError is returned.
func Build(class *v1alpha1.NamespaceClass, namespace string, mapper meta.RESTMapper) ([]*unstructured.Unstructured, error) {
	v := validator{mapper: mapper, firstIndex: make(map[inventory.Ref]int)}
	items := make([]*unstructured.Unstructured, 0, len(class.Spec.Resources))
	for i, raw := range class.Spec.Resources {
		item, err := decode(raw)
		if err != nil {
			v.addProblem(itemLabel(i, nil), "cannot be decoded: %v", err)
			continue
		}
		v.checkItem(i, item)
		items = append(items, item)
	}
	if len(v.problems) > 0 {
		return nil, &InvalidClassError{Problems: v.problems}
	}
	if len(v.lookupErrs) > 0 {
		return nil, errors.Join(v.lookupErrs...)
	}

	objs := make([]*unstructured.Unstructured, len(items))
	for i, item := range items {
		objs[i] = render(item, class, namespace)
	}
	return objs, nil
}

// decode uses MarshalJSON and UnmarshalJSON, not json.Unmarshal(raw.Raw): they handle every form
// of RawExtension, and they keep whole numbers as int64 instead of rounding them as float64.
func decode(raw runtime.RawExtension) (*unstructured.Unstructured, error) {
	data, err := raw.MarshalJSON()
	if err != nil {
		return nil, err
	}
	item := &unstructured.Unstructured{}
	if err := item.UnmarshalJSON(data); err != nil {
		return nil, explainDecodeError(data, err)
	}
	return item, nil
}

// explainDecodeError finds the real cause of a decode error. Unstructured.UnmarshalJSON reports a
// missing kind when the apiVersion cannot be parsed, even if the kind is set. So when the JSON has
// a kind, the apiVersion is the cause.
func explainDecodeError(data []byte, err error) error {
	var typeMeta metav1.TypeMeta
	if runtime.IsMissingKind(err) && json.Unmarshal(data, &typeMeta) == nil && typeMeta.Kind != "" {
		return fmt.Errorf("apiVersion %q is not valid", typeMeta.APIVersion)
	}
	return err
}

// itemLabel names an item in messages, for example "spec.resources[2] (ConfigMap/network-info)".
func itemLabel(index int, item *unstructured.Unstructured) string {
	label := fmt.Sprintf("spec.resources[%d]", index)
	switch {
	case item == nil:
		return label
	case item.GetName() == "":
		return fmt.Sprintf("%s (%s)", label, item.GetKind())
	default:
		return fmt.Sprintf("%s (%s/%s)", label, item.GetKind(), item.GetName())
	}
}

type validator struct {
	mapper     meta.RESTMapper
	firstIndex map[inventory.Ref]int // index of the first item with this group, kind and name
	problems   []string
	lookupErrs []error
}

func (v *validator) addProblem(label, format string, args ...any) {
	v.problems = append(v.problems, label+": "+fmt.Sprintf(format, args...))
}

// checkItem records every problem of one item. All checks run, so the user sees all problems of
// the class at once.
func (v *validator) checkItem(index int, item *unstructured.Unstructured) {
	label := itemLabel(index, item)
	name := item.GetName()

	if name == "" {
		v.addProblem(label, "metadata.name is required")
	}
	if item.GetGenerateName() != "" {
		v.addProblem(label, "metadata.generateName is not supported; set metadata.name")
	}
	if namespace := item.GetNamespace(); namespace != "" {
		v.addProblem(label, "metadata.namespace must be empty, but is %q; the class creates the object in every namespace that uses it", namespace)
	}

	gvk := item.GroupVersionKind()
	if gvk.Version == "" {
		v.addProblem(label, "apiVersion %q is not valid; use version or group/version", item.GetAPIVersion())
		return
	}

	if name != "" {
		ref := inventory.RefOf(item)
		if first, seen := v.firstIndex[ref]; seen {
			v.addProblem(label, "duplicates spec.resources[%d]; group, kind and name must be unique (the version is ignored)", first)
		} else {
			v.firstIndex[ref] = index
		}
		if createdByKubernetes.Has(ref) {
			v.addProblem(label, "%s is created by Kubernetes in every namespace and cannot be part of a class", ref)
		}
	}

	mapping, err := v.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	switch {
	case err != nil:
		v.lookupErrs = append(v.lookupErrs, fmt.Errorf("%s: %w", label, err))
	case mapping.Scope.Name() != meta.RESTScopeNameNamespace:
		v.addProblem(label, "kind %s is cluster-scoped; a class can only create namespaced objects", gvk.Kind)
	}
}

// render builds the object to apply: every top-level field of the item except metadata and
// status, plus new metadata made only of the name, the namespace, the item's labels and
// annotations, the class label and an owner reference to the class. Server-set fields in the
// item, such as uid or an owner reference of its own, are dropped.
func render(item *unstructured.Unstructured, class *v1alpha1.NamespaceClass, namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: make(map[string]any, len(item.Object))}
	for key, value := range item.Object {
		if key != "metadata" && key != "status" {
			obj.Object[key] = runtime.DeepCopyJSONValue(value)
		}
	}

	labels := item.GetLabels()
	if labels == nil {
		labels = make(map[string]string, 1)
	}
	labels[v1alpha1.ManagedByClassLabel] = class.Name

	obj.SetName(item.GetName())
	obj.SetNamespace(namespace)
	obj.SetLabels(labels)
	if annotations := item.GetAnnotations(); len(annotations) > 0 {
		obj.SetAnnotations(annotations)
	}
	// The class's TypeMeta is often empty when the class was read through a typed client.
	obj.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: v1alpha1.GroupVersion.String(),
		Kind:       v1alpha1.NamespaceClassKind,
		Name:       class.Name,
		UID:        class.UID,
		Controller: new(true),
	}})
	return obj
}
