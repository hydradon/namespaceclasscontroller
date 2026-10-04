package controller

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/hydradon/namespaceclasscontroller/internal/inventory"
)

// prune deletes the objects in stale that the controller manages. keep holds the objects whose
// deletion failed; they must stay in the annotation so a later run deletes them.
func (r *NamespaceReconciler) prune(ctx context.Context, ns *corev1.Namespace, stale inventory.Set) (keep inventory.Set, err error) {
	keep = inventory.Set{}
	var errs []error
	// Sorted, so deletions run in a stable order.
	for _, ref := range inventory.Sorted(stale) {
		if err := r.deleteObject(ctx, ns, ref); err != nil {
			keep.Insert(ref)
			errs = append(errs, err)
		}
	}
	return keep, errors.Join(errs...)
}

// deleteObject deletes one object. There is nothing to do when the object or its kind is gone,
// when the object is not managed by the controller, or when it is already being deleted.
func (r *NamespaceReconciler) deleteObject(ctx context.Context, ns *corev1.Namespace, ref inventory.Ref) error {
	mapping, err := r.RESTMapper().RESTMapping(ref.GroupKind())
	switch {
	case meta.IsNoMatchError(err):
		return nil
	case err != nil:
		return r.deleteFailed(ns, nil, ref, err)
	}

	// A PartialObjectMetadata cannot be used for the delete call: decoding the response fails for
	// kinds that are not in the scheme.
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(mapping.GroupVersionKind)
	obj.SetNamespace(ns.Name)
	obj.SetName(ref.Name)

	live := &metav1.PartialObjectMetadata{}
	live.SetGroupVersionKind(mapping.GroupVersionKind)
	err = r.APIReader.Get(ctx, client.ObjectKeyFromObject(obj), live)
	switch {
	case apierrors.IsNotFound(err):
		return nil
	case err != nil:
		return r.deleteFailed(ns, obj, ref, err)
	case !inventory.IsManaged(live), !live.DeletionTimestamp.IsZero():
		return nil
	}

	uid, resourceVersion := live.GetUID(), live.GetResourceVersion()
	obj.SetUID(uid)
	err = r.Delete(ctx, obj,
		client.Preconditions{UID: &uid, ResourceVersion: &resourceVersion},
		client.PropagationPolicy(metav1.DeletePropagationBackground))
	switch {
	case apierrors.IsNotFound(err):
		return nil
	case err != nil:
		return r.deleteFailed(ns, obj, ref, err)
	}
	r.event(ns, obj, corev1.EventTypeNormal, ReasonDeleted, actionDelete, "deleted "+ref.String())
	return nil
}

// deleteFailed records a DeleteFailed event. obj is nil when the kind could not be looked up.
func (r *NamespaceReconciler) deleteFailed(ns *corev1.Namespace, obj *unstructured.Unstructured, ref inventory.Ref, err error) error {
	note := fmt.Sprintf("cannot delete %s: %v", ref, err)
	if obj == nil {
		r.event(ns, nil, corev1.EventTypeWarning, ReasonDeleteFailed, actionDelete, note)
	} else {
		r.event(ns, obj, corev1.EventTypeWarning, ReasonDeleteFailed, actionDelete, note)
	}
	return fmt.Errorf("delete %s: %w", ref, err)
}
