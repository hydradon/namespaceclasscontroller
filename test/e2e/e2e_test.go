//go:build e2e

package e2e

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/hydradon/namespaceclasscontroller/api/v1alpha1"
	"github.com/hydradon/namespaceclasscontroller/test/utils"
)

const (
	// managerNamespace is the namespace the controller is deployed in.
	managerNamespace = "namespaceclass-system"
	// classCRD is the name of the NamespaceClass CustomResourceDefinition.
	classCRD = "namespaceclasses.namespaceclass.akuity.io"

	publicNetwork   = "public-network"
	internalNetwork = "internal-network"
	teamBaseline    = "team-baseline"

	webPortal = "web-portal"
	billing   = "billing"
	teamA     = "team-a"
	legacy    = "legacy"

	// The sample classes and namespaces that the specs apply, in config/samples.
	publicNetworkSample   = "namespaceclass_v1alpha1_public-network.yaml"
	internalNetworkSample = "namespaceclass_v1alpha1_internal-network.yaml"
	teamBaselineSample    = "namespaceclass_v1alpha1_team-baseline.yaml"
	webPortalSample       = "namespace_web-portal.yaml"
	billingSample         = "namespace_billing.yaml"
	teamASample           = "namespace_team-a.yaml"

	// Paths that kubectl reads from an object.
	uidPath     = "{.metadata.uid}"
	ownersPath  = "{.metadata.ownerReferences}"
	vpnCIDRPath = "{.spec.ingress[0].from[0].ipBlock.cidr}"

	// legacyConfigMap is made by hand and has the name of an object that internal-network creates.
	legacyConfigMap = `apiVersion: v1
kind: ConfigMap
metadata:
  name: network-info
  namespace: legacy
data:
  owner: me
`
)

// The kubectl resource names that the specs list in a namespace, and the objects that the sample
// classes create, written as Kind/name.
var (
	networkResources = []string{"networkpolicy", "configmap"}
	teamResources    = []string{"serviceaccount", "resourcequota", "limitrange", "rolebinding"}

	publicObjects     = []string{"NetworkPolicy/ingress"}
	internalObjects   = []string{"NetworkPolicy/ingress", "NetworkPolicy/egress", "ConfigMap/network-info"}
	internalV2Objects = append(slices.Clone(internalObjects), "NetworkPolicy/allow-monitoring")
	teamObjects       = []string{
		"ServiceAccount/deployer", "ResourceQuota/compute", "LimitRange/defaults", "RoleBinding/deployer-view",
	}
)

var _ = Describe("NamespaceClass controller", Ordered, func() {
	// Before running the tests, set up the environment by creating the namespace, enforcing the
	// restricted security policy on it, installing the CRD, and deploying the controller.
	BeforeAll(func() {
		By("creating the manager namespace")
		_, err := utils.Kubectl("create", "ns", managerNamespace)
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

	// After all tests have been executed, clean up by deleting the sample namespaces, undeploying
	// the controller, uninstalling the CRD, and deleting the manager namespace.
	AfterAll(func() {
		By("deleting the sample namespaces")
		_, _ = utils.Kubectl("delete", "ns", webPortal, billing, teamA, legacy, "--ignore-not-found")

		By("undeploying the controller-manager")
		_, _ = utils.Run(exec.Command("make", "undeploy"))

		By("uninstalling CRDs")
		_, _ = utils.Run(exec.Command("make", "uninstall"))

		By("removing manager namespace")
		_, _ = utils.Kubectl("delete", "ns", managerNamespace, "--ignore-not-found")
	})

	// After each test, check for failures and collect logs, events, and pod descriptions for debugging.
	AfterEach(func() {
		if !CurrentSpecReport().Failed() {
			return
		}
		printKubectl("controller logs", "logs", "-l", "control-plane=controller-manager", "-n", managerNamespace)
		printKubectl("Kubernetes events", "get", "events", "-A", "--sort-by=.lastTimestamp")
		printKubectl("controller pod description", "describe", "pod", "-l", "control-plane=controller-manager",
			"-n", managerNamespace)
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	It("runs the controller pod", func() {
		Eventually(func(g Gomega) {
			phases, err := controllerPodField("{.items[*].status.phase}")
			g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
			g.Expect(strings.Fields(phases)).To(ConsistOf("Running"), "expected one controller pod, running")

			ready, err := controllerPodField(`{.items[*].status.conditions[?(@.type=="Ready")].status}`)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ready).To(Equal("True"), "controller pod not ready")
		}).Should(Succeed())
	})

	It("creates the objects of a class in a namespace that uses it", func() {
		By("waiting for the CRD to be established")
		_, err := utils.Kubectl("wait", "--for=condition=Established", "crd/"+classCRD, "--timeout=60s")
		Expect(err).NotTo(HaveOccurred())

		By("applying the sample classes and namespaces")
		for _, file := range []string{
			publicNetworkSample, internalNetworkSample, teamBaselineSample,
			webPortalSample, billingSample, teamASample,
		} {
			_, err := utils.Kubectl("apply", "-f", filepath.Join("config", "samples", file))
			Expect(err).NotTo(HaveOccurred())
		}

		By("waiting for the NetworkPolicy of web-portal")
		Eventually(managedObjects(webPortal, networkResources...)).Should(ConsistOf(publicObjects))
		ingress := "networkpolicy/ingress"
		Expect(field(ingress, webPortal, vpnCIDRPath)(Default)).To(Equal("0.0.0.0/0"))

		By("checking that the object is marked as created by the class")
		Expect(field(ingress, webPortal, labelPath(v1alpha1.ManagedByClassLabel))(Default)).To(Equal(publicNetwork))
		owner := "{range .metadata.ownerReferences[*]}{.kind}/{.name}/{.controller}{end}"
		Expect(field(ingress, webPortal, owner)(Default)).To(Equal("NamespaceClass/" + publicNetwork + "/true"))

		By("checking that the namespace lists the object")
		recorded := field("namespace/"+webPortal, "", annotationPath(v1alpha1.ManagedResourcesAnnotation))
		Expect(recorded(Default)).To(MatchJSON(`[{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]`))
	})

	It("lists the events of a namespace", func() {
		Eventually(func(g Gomega) {
			events, err := utils.Kubectl(eventsOf(webPortal)...)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(events).To(ContainSubstring("Created"))
			g.Expect(events).To(ContainSubstring("created NetworkPolicy.networking.k8s.io/ingress"))
			_, _ = fmt.Fprintf(GinkgoWriter, "events of %s:\n%s\n", webPortal, events)
		}).Should(Succeed())
	})

	It("switches a namespace to another class and keeps the objects that both classes define", func() {
		ingress := "networkpolicy/ingress"
		ingressUID := field(ingress, webPortal, uidPath)(Default)

		_, err := utils.Kubectl("label", "ns", webPortal, v1alpha1.ClassLabel+"="+internalNetwork, "--overwrite")
		Expect(err).NotTo(HaveOccurred())

		Eventually(field(ingress, webPortal, vpnCIDRPath)).Should(Equal("10.8.0.0/16"))
		Eventually(managedObjects(webPortal, networkResources...)).Should(ConsistOf(internalObjects))
		Expect(field(ingress, webPortal, uidPath)(Default)).To(Equal(ingressUID),
			"the object that both classes define must be updated in place, not re-created")
		Expect(field(ingress, webPortal, "{.metadata.ownerReferences[*].name}")(Default)).To(Equal(internalNetwork))
	})

	It("applies the edits of a class to every namespace that uses it", func() {
		By("applying a class with a new VPN range and one more policy")
		_, err := utils.Kubectl("apply", "-f", filepath.Join("docs", "demo", "internal-network-v2.yaml"))
		Expect(err).NotTo(HaveOccurred())
		for _, ns := range []string{webPortal, billing} {
			Eventually(field("networkpolicy/ingress", ns, vpnCIDRPath)).Should(Equal("10.9.0.0/16"))
			Eventually(field("configmap/network-info", ns, "{.data.vpnCIDR}")).Should(Equal("10.9.0.0/16"))
			Eventually(managedObjects(ns, networkResources...)).Should(ConsistOf(internalV2Objects))
		}

		By("applying the original class again")
		_, err = utils.Kubectl("apply", "-f", filepath.Join("config", "samples", internalNetworkSample))
		Expect(err).NotTo(HaveOccurred())
		for _, ns := range []string{webPortal, billing} {
			Eventually(field("networkpolicy/ingress", ns, vpnCIDRPath)).Should(Equal("10.8.0.0/16"))
			Eventually(managedObjects(ns, networkResources...)).Should(ConsistOf(internalObjects))
		}
	})

	It("restores an object that is deleted or edited by hand", func() {
		By("deleting a NetworkPolicy")
		egressUID := field("networkpolicy/egress", webPortal, uidPath)(Default)
		_, err := utils.Kubectl("delete", "networkpolicy", "egress", "-n", webPortal)
		Expect(err).NotTo(HaveOccurred())
		Eventually(field("networkpolicy/egress", webPortal, uidPath)).WithTimeout(30 * time.Second).
			Should(And(Not(BeEmpty()), Not(Equal(egressUID))))

		By("emptying the ingress rules of a NetworkPolicy")
		out, err := utils.Kubectl("patch", "networkpolicy", "ingress", "-n", webPortal, "--type=merge",
			"-p", `{"spec":{"ingress":[]}}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).NotTo(ContainSubstring("(no change)"))
		Eventually(field("networkpolicy/ingress", webPortal, vpnCIDRPath)).WithTimeout(30 * time.Second).
			Should(Equal("10.8.0.0/16"))
	})

	It("leaves an object it did not create alone, also when the namespace opts out", func() {
		By("creating a namespace that has an object with a name the class wants")
		_, err := utils.Kubectl("create", "ns", legacy)
		Expect(err).NotTo(HaveOccurred())
		Expect(utils.KubectlApply(legacyConfigMap)).To(Succeed())

		By("labeling the namespace with the class")
		_, err = utils.Kubectl("label", "ns", legacy, v1alpha1.ClassLabel+"="+internalNetwork)
		Expect(err).NotTo(HaveOccurred())
		Eventually(managedObjects(legacy, networkResources...)).
			Should(ConsistOf("NetworkPolicy/ingress", "NetworkPolicy/egress"))
		Eventually(func(g Gomega) {
			events, err := utils.Kubectl(eventsOf(legacy)...)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(events).To(ContainSubstring("Conflict"))
			g.Expect(events).To(ContainSubstring("ConfigMap/network-info already exists"))
		}).Should(Succeed())
		configMapData := field("configmap/network-info", legacy, "{.data}")
		Expect(configMapData(Default)).To(MatchJSON(`{"owner":"me"}`))
		recorded := field("namespace/"+legacy, "", annotationPath(v1alpha1.ManagedResourcesAnnotation))
		Expect(recorded(Default)).To(MatchJSON(`[
			{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"egress"},
			{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}
		]`))

		By("removing the label")
		_, err = utils.Kubectl("label", "ns", legacy, v1alpha1.ClassLabel+"-")
		Expect(err).NotTo(HaveOccurred())
		Eventually(managedObjects(legacy, networkResources...)).Should(BeEmpty())
		Expect(configMapData(Default)).To(MatchJSON(`{"owner":"me"}`))
	})

	It("creates objects of any kind", func() {
		Eventually(managedObjects(teamA, teamResources...)).Should(ConsistOf(teamObjects))
	})

	It("deletes the objects with the real garbage collector when its class is deleted", func() {
		deployer := "serviceaccount/deployer"

		By("deleting the class with --cascade=orphan")
		_, err := utils.Kubectl("delete", "nsclass", teamBaseline, "--cascade=orphan", "--timeout=2m")
		Expect(err).NotTo(HaveOccurred())
		Eventually(field(deployer, teamA, ownersPath)).Should(BeEmpty())
		Consistently(managedObjects(teamA, teamResources...)).WithTimeout(10 * time.Second).
			WithPolling(time.Second).Should(ConsistOf(teamObjects))

		By("creating the class again")
		_, err = utils.Kubectl("apply", "-f", filepath.Join("config", "samples", teamBaselineSample))
		Expect(err).NotTo(HaveOccurred())
		classUID := field("nsclass/"+teamBaseline, "", uidPath)(Default)
		Expect(classUID).NotTo(BeEmpty())
		Eventually(field(deployer, teamA, "{.metadata.ownerReferences[*].uid}")).Should(Equal(classUID))

		By("deleting the class")
		_, err = utils.Kubectl("delete", "nsclass", teamBaseline, "--timeout=2m")
		Expect(err).NotTo(HaveOccurred())
		Eventually(managedObjects(teamA, teamResources...)).Should(BeEmpty())
	})

	It("deletes the objects of a namespace when its label is removed", func() {
		_, err := utils.Kubectl("label", "ns", webPortal, v1alpha1.ClassLabel+"-")
		Expect(err).NotTo(HaveOccurred())

		Eventually(managedObjects(webPortal, networkResources...)).Should(BeEmpty())
		recorded := field("namespace/"+webPortal, "", annotationPath(v1alpha1.ManagedResourcesAnnotation))
		Eventually(recorded).Should(BeEmpty())
	})
})

// eventsOf returns the kubectl arguments that list the events of a namespace. Events about a
// cluster-scoped object such as a Namespace are stored in the default namespace.
func eventsOf(namespace string) []string {
	return []string{"events", "-n", "default", "--for", "namespace/" + namespace}
}

// controllerPodField reads a field of the controller pods with a kubectl JSONPath expression.
func controllerPodField(jsonPath string) (string, error) {
	return utils.Kubectl("get", "pods", "-l", "control-plane=controller-manager", "-n", managerNamespace,
		"-o", "jsonpath="+jsonPath)
}

// field returns a function for Eventually that reads one field of an object with a kubectl JSONPath
// expression. The resource is written like kubectl does, for example "networkpolicy/ingress". The
// namespace is empty for a cluster-scoped object.
func field(resource, namespace, jsonPath string) func(g Gomega) string {
	return func(g Gomega) string {
		args := []string{"get", resource, "-o", "jsonpath=" + jsonPath}
		if namespace != "" {
			args = append(args, "-n", namespace)
		}
		out, err := utils.Kubectl(args...)
		g.Expect(err).NotTo(HaveOccurred())
		return out
	}
}

// managedObjects returns a function for Eventually that lists the objects of the given kinds that
// the controller created in the namespace, written as Kind/name.
func managedObjects(namespace string, resources ...string) func(g Gomega) []string {
	return func(g Gomega) []string {
		out, err := utils.Kubectl("get", strings.Join(resources, ","), "-n", namespace,
			"-l", v1alpha1.ManagedByClassLabel,
			"-o", `jsonpath={range .items[*]}{.kind}/{.metadata.name}{"\n"}{end}`)
		g.Expect(err).NotTo(HaveOccurred())
		return utils.GetNonEmptyLines(out)
	}
}

func labelPath(key string) string {
	return "{.metadata.labels." + escapeDots(key) + "}"
}

func annotationPath(key string) string {
	return "{.metadata.annotations." + escapeDots(key) + "}"
}

// escapeDots makes a label or annotation key usable in a JSONPath expression.
func escapeDots(key string) string {
	return strings.ReplaceAll(key, ".", `\.`)
}

// printKubectl writes the output of a kubectl command to the test output, for debugging a failure.
func printKubectl(title string, args ...string) {
	By("fetching the " + title)
	out, err := utils.Kubectl(args...)
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get the %s: %s\n", title, err)
		return
	}
	_, _ = fmt.Fprintf(GinkgoWriter, "%s:\n%s\n", title, out)
}
