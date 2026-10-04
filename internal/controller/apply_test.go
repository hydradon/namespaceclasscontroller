package controller

import (
	"errors"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIsNamespaceTerminating(t *testing.T) {
	configMaps := schema.GroupResource{Resource: "configmaps"}
	const objName = "settings"
	// The error of the API server when an object is created in a namespace that is being deleted:
	// Forbidden, with the cause NamespaceTerminating.
	terminating := apierrors.NewForbidden(configMaps, objName,
		errors.New("unable to create new content in namespace ns because it is being terminated"))
	terminating.ErrStatus.Details.Causes = append(terminating.ErrStatus.Details.Causes, metav1.StatusCause{
		Type:    corev1.NamespaceTerminatingCause,
		Message: "namespace ns is being terminated",
		Field:   "metadata.namespace",
	})

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"namespace is being deleted", terminating, true},
		{"the same error, wrapped", fmt.Errorf("apply ConfigMap/settings: %w", terminating), true},
		{"forbidden for another reason", apierrors.NewForbidden(configMaps, objName, errors.New("access denied")), false},
		{"another API error", apierrors.NewNotFound(configMaps, objName), false},
		{"no error", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNamespaceTerminating(tc.err); got != tc.want {
				t.Errorf("isNamespaceTerminating(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
