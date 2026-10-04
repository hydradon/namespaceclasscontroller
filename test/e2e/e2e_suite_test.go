//go:build e2e

package e2e

import (
	"cmp"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/hydradon/namespaceclasscontroller/test/utils"
)

// managerImage is the controller image that is built and loaded into the kind cluster. The
// Makefile passes its KIND_IMG, the image that `make kind-deploy` uses.
var managerImage = cmp.Or(os.Getenv("KIND_IMG"), "namespaceclass-controller:dev")

// TestE2E runs the e2e test suite against a kind cluster. Run it with `make test-e2e`: the target
// creates the cluster and points KUBECONFIG at a kubeconfig file that holds only that cluster.
// The spec that runs hack/demo.sh has the label "demo"; `make test-e2e` skips it unless
// E2E_LABEL_FILTER selects it.
//
// To enable kubectl kuberc (use custom kubectl configurations), set: KUBECTL_KUBERC=true
// By default, kuberc is disabled to ensure consistent test behavior across different environments.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting namespaceclass e2e test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	By("checking that kubectl uses the kind cluster")
	Expect(os.Getenv("KUBECONFIG")).NotTo(BeEmpty(), "run via `make test-e2e`; refusing to use the default kubeconfig")
	out, err := utils.Run(exec.Command("kubectl", "config", "current-context"))
	Expect(err).NotTo(HaveOccurred())
	Expect(strings.TrimSpace(out)).To(Equal("kind-" + os.Getenv("KIND_CLUSTER")))
	// Not an AfterSuite: Ginkgo runs AfterSuite also when the check above fails, and the cleanup
	// targets use whatever cluster KUBECONFIG points at. A DeferCleanup exists only once the
	// check has passed.
	DeferCleanup(removeController)

	configureKubectlKubeRC()

	By("building the manager image")
	_, err = utils.Run(exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", managerImage)))
	Expect(err).NotTo(HaveOccurred(), "Failed to build the manager image")

	By("loading the manager image on Kind")
	err = utils.LoadImageToKindClusterWithName(managerImage)
	Expect(err).NotTo(HaveOccurred(), "Failed to load the manager image into Kind")

	// The controller is deployed here and not in a container, so that every spec has it, whatever
	// the label filter selects.
	By("creating the manager namespace")
	_, err = utils.Kubectl("create", "ns", managerNamespace)
	Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

	By("labeling the namespace to enforce the restricted security policy")
	_, err = utils.Kubectl("label", "--overwrite", "ns", managerNamespace,
		"pod-security.kubernetes.io/enforce=restricted")
	Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

	By("installing CRDs")
	_, err = utils.Run(exec.Command("make", "install"))
	Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

	By("deploying the controller-manager")
	_, err = utils.Run(exec.Command("make", "deploy", "IMG="+managerImage))
	Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
})

func removeController() {
	By("undeploying the controller-manager")
	_, _ = utils.Run(exec.Command("make", "undeploy"))

	By("uninstalling CRDs")
	_, _ = utils.Run(exec.Command("make", "uninstall"))

	By("removing manager namespace")
	_, _ = utils.Kubectl("delete", "ns", managerNamespace, "--ignore-not-found")
}

// After a spec fails, print the controller logs, the events and the controller pod description.
var _ = AfterEach(func() {
	if !CurrentSpecReport().Failed() {
		return
	}
	printKubectl("controller logs", "logs", "-l", "control-plane=controller-manager", "-n", managerNamespace,
		"--tail=-1")
	printKubectl("Kubernetes events", "get", "events", "-A", "--sort-by=.lastTimestamp")
	printKubectl("controller pod description", "describe", "pod", "-l", "control-plane=controller-manager",
		"-n", managerNamespace)
})

// Disable kubectl kuberc by default for test isolation.
// This prevents local kubectl configurations from affecting test behavior.
// To enable kuberc, set: KUBECTL_KUBERC=true
func configureKubectlKubeRC() {
	if os.Getenv("KUBECTL_KUBERC") != "true" {
		By("disabling kubectl kuberc for test isolation")
		err := os.Setenv("KUBECTL_KUBERC", "false")
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to disable kubectl kuberc")
		_, _ = fmt.Fprintf(GinkgoWriter,
			"kubectl kuberc disabled for consistent test behavior (override with KUBECTL_KUBERC=true)\n")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "kubectl kuberc enabled (KUBECTL_KUBERC=true)\n")
	}
}
