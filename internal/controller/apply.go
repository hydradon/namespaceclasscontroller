package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/hydradon/namespaceclasscontroller/internal/inventory"
)

// applyItem is a desired object that the controller may apply, with the metadata of the live
// object that was read before anything was written. live is nil when the object does not exist.
type applyItem struct {
	obj  *unstructured.Unstructured
	ref  inventory.Ref
	live *metav1.PartialObjectMetadata
}

// inspect reads the live metadata of every desired object before the annotation is written. An
// object that exists but is not managed by the controller is a conflict: it gets a Conflict event
// and is left out of the run, so it is never listed in the annotation and never changed. An object
// whose read failed is not applied in this run; its error is returned in failed.
func (r *NamespaceReconciler) inspect(ctx context.Context, ns *corev1.Namespace, desired []*unstructured.Unstructured) (items []applyItem, conflicts inventory.Set, failed []error) {
	conflicts = inventory.Set{}
	for _, obj := range desired {
		ref := inventory.RefOf(obj)
		live := &metav1.PartialObjectMetadata{}
		live.SetGroupVersionKind(obj.GroupVersionKind())
		err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(obj), live)
		switch {
		case apierrors.IsNotFound(err):
			items = append(items, applyItem{obj: obj, ref: ref})
		case err != nil:
			failed = append(failed, r.applyFailed(ns, obj, ref, err))
		case !inventory.IsManaged(live):
			r.event(ns, live, corev1.EventTypeWarning, ReasonConflict, actionApply,
				fmt.Sprintf("%s already exists and was not created by %s; it is left unchanged", ref, fieldManager))
			conflicts.Insert(ref)
		default:
			items = append(items, applyItem{obj: obj, ref: ref, live: live})
		}
	}
	return items, conflicts, failed
}

// applyAll applies the items in the order of the class and returns the errors. A failed item does
// not stop the others, unless the namespace started terminating: then nothing more can be created.
func (r *NamespaceReconciler) applyAll(ctx context.Context, ns *corev1.Namespace, items []applyItem) []error {
	var errs []error
	for _, item := range items {
		err := r.apply(ctx, ns, item)
		if err == nil {
			continue
		}
		errs = append(errs, err)
		if isNamespaceTerminating(err) {
			break
		}
	}
	return errs
}

func (r *NamespaceReconciler) apply(ctx context.Context, ns *corev1.Namespace, item applyItem) error {
	// The server's response replaces the content of item.obj, so its resourceVersion is the new one.
	err := r.Apply(ctx, client.ApplyConfigurationFromUnstructured(item.obj), client.FieldOwner(fieldManager), client.ForceOwnership)
	switch {
	case isNamespaceTerminating(err):
		return err
	case err != nil:
		return r.applyFailed(ns, item.obj, item.ref, err)
	case item.live == nil:
		r.event(ns, item.obj, corev1.EventTypeNormal, ReasonCreated, actionApply, "created "+item.ref.String())
	case item.obj.GetResourceVersion() != item.live.GetResourceVersion():
		r.event(ns, item.obj, corev1.EventTypeNormal, ReasonUpdated, actionApply, "updated "+item.ref.String())
	}
	return nil
}

func (r *NamespaceReconciler) applyFailed(ns *corev1.Namespace, obj *unstructured.Unstructured, ref inventory.Ref, err error) error {
	r.event(ns, obj, corev1.EventTypeWarning, ReasonApplyFailed, actionApply, fmt.Sprintf("cannot apply %s: %v", ref, err))
	return fmt.Errorf("apply %s: %w", ref, err)
}

// isNamespaceTerminating reports whether the API server refused to create an object because its
// namespace is being deleted.
func isNamespaceTerminating(err error) bool {
	return apierrors.HasStatusCause(err, corev1.NamespaceTerminatingCause)
}
