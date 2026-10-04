// Package controller holds the reconciler that applies NamespaceClasses to Namespaces.
package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
	"github.com/hydradon/namespaceclasscontroller/internal/inventory"
	"github.com/hydradon/namespaceclasscontroller/internal/resources"
)

// fieldManager is the Server-Side Apply field manager. It must never change: Server-Side Apply
// removes a field from a live object only when this manager applied it before.
const fieldManager = "namespaceclass-controller"

// NamespaceReconciler creates the objects of a NamespaceClass in every Namespace labeled with the
// class name, keeps them in line with the class, and deletes the objects that are no longer
// wanted. It handles one Namespace per run.
type NamespaceReconciler struct {
	client.Client                      // cached reads of NamespaceClasses and the Namespace list; all writes
	APIReader     client.Reader        // live reads: the Namespace being reconciled and managed objects
	Recorder      events.EventRecorder // events about Namespaces and NamespaceClasses
}

// +kubebuilder:rbac:groups=*,resources=*,verbs=*

// Reconcile brings one Namespace in line with its class. The order of the steps keeps the
// controller safe when it stops in the middle of a run: objects that exist but are not managed
// are found before anything is written and are never listed in the managed-resources annotation,
// an object is listed before it is created, and old objects are deleted only after every item of
// the class was applied.
func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// A live read, not the cache, so the annotation written by the previous run is never missed.
	ns := &corev1.Namespace{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, ns); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !ns.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	previous, err := inventory.Parse(ns.Annotations[v1alpha1.ManagedResourcesAnnotation])
	if err != nil {
		// Nothing is deleted based on a list that cannot be read.
		r.event(ns, nil, corev1.EventTypeWarning, ReasonInvalidAnnotation, actionReconcile, fmt.Sprintf(
			"annotation %s cannot be read (%v); it is treated as empty, so objects it listed that are no longer wanted are left in place",
			v1alpha1.ManagedResourcesAnnotation, err))
		previous = inventory.Set{}
	}

	desired, proceed, err := r.desiredObjects(ctx, ns)
	if !proceed {
		return ctrl.Result{}, err
	}

	want := inventory.RefsOf(desired)
	items, conflicts, failed := r.inspect(ctx, ns, desired)
	owned := want.Difference(conflicts)
	intent := previous.Union(owned)
	if err := r.recordIfChanged(ctx, ns, intent); err != nil {
		return ctrl.Result{}, err
	}

	applyErrs := slices.Concat(failed, r.applyAll(ctx, ns, items))
	if slices.ContainsFunc(applyErrs, isNamespaceTerminating) {
		return ctrl.Result{}, nil
	}
	if len(applyErrs) > 0 {
		// Old objects stay until every item applies, so a rejected item never leaves the
		// Namespace with fewer objects than before.
		return ctrl.Result{}, errors.Join(applyErrs...)
	}

	keep, pruneErr := r.prune(ctx, ns, intent.Difference(want))
	if err := r.recordIfChanged(ctx, ns, owned.Union(keep)); err != nil {
		return ctrl.Result{}, errors.Join(pruneErr, err)
	}
	return ctrl.Result{}, pruneErr
}

// desiredObjects returns the objects the Namespace should have. proceed is false when the run must
// stop without writing anything; err is set when the run should be retried.
func (r *NamespaceReconciler) desiredObjects(ctx context.Context, ns *corev1.Namespace) (desired []*unstructured.Unstructured, proceed bool, err error) {
	className, labeled := ns.Labels[v1alpha1.ClassLabel]
	if !labeled {
		// Opted out: every object recorded in the annotation is deleted.
		return nil, true, nil
	}
	if className == "" {
		r.event(ns, nil, corev1.EventTypeWarning, ReasonClassNotFound, actionReconcile,
			fmt.Sprintf("label %s is empty; nothing was changed", v1alpha1.ClassLabel))
		return nil, false, nil
	}

	class := &v1alpha1.NamespaceClass{}
	if err := r.Get(ctx, client.ObjectKey{Name: className}, class); err != nil {
		if apierrors.IsNotFound(err) {
			r.event(ns, nil, corev1.EventTypeWarning, ReasonClassNotFound, actionReconcile,
				fmt.Sprintf("NamespaceClass %q does not exist; nothing was changed", className))
			return nil, false, nil
		}
		return nil, false, err
	}
	if !class.DeletionTimestamp.IsZero() {
		r.event(ns, class, corev1.EventTypeWarning, ReasonClassNotFound, actionReconcile,
			fmt.Sprintf("NamespaceClass %q is being deleted; nothing was changed", className))
		return nil, false, nil
	}

	desired, err = resources.Build(class, ns.Name, r.RESTMapper())
	if invalid, ok := errors.AsType[*resources.InvalidClassError](err); ok {
		// Only an edit of the class fixes it, so the run is not retried.
		note := fmt.Sprintf("NamespaceClass %q is invalid, so nothing was changed: %v", class.Name, invalid)
		r.event(ns, class, corev1.EventTypeWarning, ReasonInvalidClass, actionReconcile, note)
		r.event(class, ns, corev1.EventTypeWarning, ReasonInvalidClass, actionReconcile, note)
		return nil, false, nil
	}
	if err != nil {
		// Usually a kind whose CRD is not installed yet; retried with backoff.
		r.event(ns, class, corev1.EventTypeWarning, ReasonApplyFailed, actionApply,
			fmt.Sprintf("cannot apply NamespaceClass %q, so nothing was changed: %v", class.Name, err))
		return nil, false, err
	}
	return desired, true, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("namespaceclass").
		For(&corev1.Namespace{}, builder.WithPredicates(namespacePredicate())).
		Watches(&v1alpha1.NamespaceClass{}, handler.EnqueueRequestsFromMapFunc(r.namespacesForClass),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WithOptions(controller.Options{RateLimiter: rateLimiter()}).
		Complete(r)
}

// rateLimiter is the controller-runtime default rate limiter, with the retry backoff of one
// Namespace capped at 5 minutes instead of about 16 minutes.
func rateLimiter() workqueue.TypedRateLimiter[reconcile.Request] {
	return workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](5*time.Millisecond, 5*time.Minute),
		&workqueue.TypedBucketRateLimiter[reconcile.Request]{Limiter: rate.NewLimiter(10, 100)},
	)
}
