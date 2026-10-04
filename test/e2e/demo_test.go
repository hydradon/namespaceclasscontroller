//go:build e2e

package e2e

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/hydradon/namespaceclasscontroller/test/utils"
)

// demoOutput is the file, from the project root, that receives the output of hack/demo.sh. The
// expected output in docs/demo.md is copied from it.
const demoOutput = "bin/demo-output.txt"

// The demo spec checks that the walkthrough in docs/demo.md still works. `make test-e2e` skips it;
// run it alone on a new cluster with:
//
//	make test-e2e KIND_CLUSTER=namespaceclass-demo E2E_LABEL_FILTER=demo
var _ = Describe("Demo", Label("demo"), func() {
	It("runs every step of hack/demo.sh", func() {
		DeferCleanup(deleteSampleObjects)

		dir, err := utils.GetProjectDir()
		Expect(err).NotTo(HaveOccurred())
		output, err := os.Create(filepath.Join(dir, demoOutput))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(output.Close)

		By("running hack/demo.sh without pauses; the output goes to " + demoOutput)
		cmd := exec.Command(filepath.Join(dir, "hack", "demo.sh"))
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "NONINTERACTIVE=1")
		// One writer for both streams keeps the error messages of kubectl in order with the rest.
		out := io.MultiWriter(output, GinkgoWriter)
		cmd.Stdout, cmd.Stderr = out, out
		Expect(cmd.Run()).To(Succeed(), "hack/demo.sh failed; see its output in %s", demoOutput)
	})
})
