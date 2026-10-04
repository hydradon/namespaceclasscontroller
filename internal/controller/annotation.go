package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
	"github.com/hydradon/namespaceclasscontroller/internal/inventory"
)

// recordIfChanged writes s to the managed-resources annotation unless the annotation already
// holds exactly this text. Comparing the text also replaces an annotation that cannot be read.
func (r *NamespaceReconciler) recordIfChanged(ctx context.Context, ns *corev1.Namespace, s inventory.Set) error {
	if inventory.Encode(s) == ns.Annotations[v1alpha1.ManagedResourcesAnnotation] {
		return nil
	}
	return r.writeAnnotation(ctx, ns, s)
}

// writeAnnotation stores s in the managed-resources annotation, or removes the annotation when s
// is empty. The patch carries the resourceVersion of ns, so it fails with a conflict when ns is
// stale. On success ns holds the patched Namespace, including its new resourceVersion.
func (r *NamespaceReconciler) writeAnnotation(ctx context.Context, ns *corev1.Namespace, s inventory.Set) error {
	orig := ns.DeepCopy()
	if value := inventory.Encode(s); value != "" {
		if ns.Annotations == nil {
			ns.Annotations = make(map[string]string, 1)
		}
		ns.Annotations[v1alpha1.ManagedResourcesAnnotation] = value
	} else {
		delete(ns.Annotations, v1alpha1.ManagedResourcesAnnotation)
	}
	patch := client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})
	if err := r.Patch(ctx, ns, patch, client.FieldOwner(fieldManager)); err != nil {
		return fmt.Errorf("write annotation %s: %w", v1alpha1.ManagedResourcesAnnotation, err)
	}
	return nil
}
