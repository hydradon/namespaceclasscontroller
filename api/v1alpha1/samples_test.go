package v1alpha1_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
	"github.com/hydradon/namespaceclasscontroller/internal/resources"
)

// repoRoot is the project root, seen from this package.
const repoRoot = "../.."

// sampleDirs are the directories, from the project root, with the manifests that the demo applies.
var sampleDirs = []string{"config/samples", "docs/demo"}

// sample is one manifest: a NamespaceClass or a Namespace.
type sample struct {
	name      string // path from the project root
	class     *v1alpha1.NamespaceClass
	namespace *corev1.Namespace
}

// TestSamplesAreValid checks every sample against a real API server. A sample class must be
// accepted, and every object it builds must be accepted too. A sample namespace must name a sample
// class.
func TestSamplesAreValid(t *testing.T) {
	samples := readSamples(t)
	var classNames []string
	for _, s := range samples {
		if s.class != nil {
			classNames = append(classNames, s.class.Name)
		}
	}

	for _, s := range samples {
		t.Run(s.name, func(t *testing.T) {
			if s.class != nil {
				checkSampleClass(t, s.class)
			} else {
				checkSampleNamespace(t, s.namespace, classNames)
			}
		})
	}
}

func checkSampleClass(t *testing.T, class *v1alpha1.NamespaceClass) {
	t.Helper()
	if err := createClass(t, class); err != nil {
		t.Fatalf("the API server rejected the class: %v", err)
	}

	// The default namespace stands in for any namespace. The mapper is the API server's own, so
	// the kinds must exist and be namespaced there.
	objs, err := resources.Build(class, metav1.NamespaceDefault, k8sClient.RESTMapper())
	if err != nil {
		t.Fatalf("building the objects of the class: %v", err)
	}
	for _, obj := range objs {
		err := k8sClient.Apply(t.Context(), client.ApplyConfigurationFromUnstructured(obj),
			client.FieldOwner("samples-test"), client.DryRunAll)
		if err != nil {
			t.Errorf("the API server rejected %s %q: %v", obj.GetKind(), obj.GetName(), err)
		}
	}
}

func checkSampleNamespace(t *testing.T, ns *corev1.Namespace, classNames []string) {
	t.Helper()
	if name := ns.Labels[v1alpha1.ClassLabel]; !slices.Contains(classNames, name) {
		t.Errorf("label %s names the class %q, which is not a sample class", v1alpha1.ClassLabel, name)
	}
	if err := k8sClient.Create(t.Context(), ns, client.DryRunAll); err != nil {
		t.Errorf("the API server rejected the namespace: %v", err)
	}
}

func readSamples(t *testing.T) []sample {
	t.Helper()
	var samples []sample
	for _, dir := range sampleDirs {
		entries, err := os.ReadDir(filepath.Join(repoRoot, dir))
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) == ".yaml" && entry.Name() != "kustomization.yaml" {
				samples = append(samples, readSample(t, dir, entry.Name()))
				found++
			}
		}
		if found == 0 {
			t.Fatalf("no sample manifests found in %s", dir)
		}
	}
	return samples
}

// readSample reads a manifest and fails on a field that the type does not have, so a misspelled
// field is not dropped without notice.
func readSample(t *testing.T, dir, file string) sample {
	t.Helper()
	name := dir + "/" + file
	data, err := os.ReadFile(filepath.Join(repoRoot, dir, file))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := yaml.ToJSON(data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var typeMeta metav1.TypeMeta
	if err := json.Unmarshal(manifest, &typeMeta); err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	s := sample{name: name}
	var into any
	switch typeMeta.Kind {
	case "NamespaceClass":
		s.class = &v1alpha1.NamespaceClass{}
		into = s.class
	case "Namespace":
		s.namespace = &corev1.Namespace{}
		into = s.namespace
	default:
		t.Fatalf("%s: kind %q is neither a NamespaceClass nor a Namespace", name, typeMeta.Kind)
	}
	decoder := json.NewDecoder(bytes.NewReader(manifest))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return s
}
