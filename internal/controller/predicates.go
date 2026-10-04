package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
)

// namespacePredicate passes the Namespace events that can change what the controller must do:
// a Namespace with the class label or the managed-resources annotation appears (every Namespace
// appears at startup), the class label is added, removed or changed, or the informer resyncs.
// The controller's own annotation writes do not change the label, so they do not pass.
func namespacePredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return usesController(e.Object)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldClass, oldLabeled := e.ObjectOld.GetLabels()[v1alpha1.ClassLabel]
			newClass, newLabeled := e.ObjectNew.GetLabels()[v1alpha1.ClassLabel]
			if oldLabeled != newLabeled || oldClass != newClass {
				return true
			}
			// A periodic resync delivers the same object version as old and new.
			return e.ObjectOld.GetResourceVersion() == e.ObjectNew.GetResourceVersion() && usesController(e.ObjectNew)
		},
		DeleteFunc: func(event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(event.GenericEvent) bool {
			return false
		},
	}
}

// usesController reports whether the Namespace has the class label or the managed-resources
// annotation.
func usesController(o client.Object) bool {
	_, labeled := o.GetLabels()[v1alpha1.ClassLabel]
	_, recorded := o.GetAnnotations()[v1alpha1.ManagedResourcesAnnotation]
	return labeled || recorded
}

// namespacesForClass returns a request for every Namespace labeled with the name of the class.
func (r *NamespaceReconciler) namespacesForClass(ctx context.Context, o client.Object) []reconcile.Request {
	list := &corev1.NamespaceList{}
	if err := r.List(ctx, list, client.MatchingLabels{v1alpha1.ClassLabel: o.GetName()}); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list the Namespaces of a NamespaceClass", "namespaceClass", o.GetName())
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for _, ns := range list.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: ns.Name}})
	}
	return requests
}
