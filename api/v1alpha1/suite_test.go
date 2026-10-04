package v1alpha1_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
)

// The envtest API server is shared by every test in this package.
var (
	testEnv    *envtest.Environment
	restConfig *rest.Config
	k8sClient  client.Client
)

func TestMain(m *testing.M) {
	os.Exit(runWithTestEnv(m))
}

func runWithTestEnv(m *testing.M) int {
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		UseExistingCluster:    new(false),
	}
	if dir := getFirstFoundEnvTestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}

	var err error
	restConfig, err = testEnv.Start()
	// Stop also cleans up a control plane that only partly started.
	defer func() {
		if stopErr := testEnv.Stop(); stopErr != nil {
			fmt.Fprintf(os.Stderr, "failed to stop the test environment: %v\n", stopErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start the test environment: %v\n", err)
		return 1
	}

	if err = v1alpha1.AddToScheme(scheme.Scheme); err != nil {
		fmt.Fprintf(os.Stderr, "failed to register the NamespaceClass types: %v\n", err)
		return 1
	}
	k8sClient, err = client.New(restConfig, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create the test client: %v\n", err)
		return 1
	}

	return m.Run()
}

// getFirstFoundEnvTestBinaryDir finds the envtest binaries under bin/k8s, so the tests
// also run from an IDE without the KUBEBUILDER_ASSETS variable that `make test` sets.
func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}

// createClass saves the class and deletes it again when the test ends.
func createClass(t *testing.T, class *v1alpha1.NamespaceClass) error {
	t.Helper()
	if err := k8sClient.Create(t.Context(), class); err != nil {
		return err
	}
	t.Cleanup(func() {
		err := k8sClient.Delete(context.WithoutCancel(t.Context()), class)
		if client.IgnoreNotFound(err) != nil {
			t.Errorf("deleting NamespaceClass %q: %v", class.Name, err)
		}
	})
	return nil
}
