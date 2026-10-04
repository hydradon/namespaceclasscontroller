# Demo guide

This guide shows every main behavior of the NamespaceClass controller in about 15 minutes. All
commands run against a local kind cluster through its own kubeconfig file, so your current kubectl
context is never used.

The [use-case pages](use-cases/README.md) explain each behavior in more detail, with the objects
before and after each step of the controller's run.

The [manual testing guide](manual-testing.md) shows how to try the controller by hand, one command
at a time, also with the controller running on your machine.

You need Go, Docker and kubectl 1.26 or newer (for `kubectl events`). The Makefile installs kind
and the other tools into `bin/`.

There are two ways to follow the guide after step 0:

- Type the commands of each step.
- Run `hack/demo.sh`. It runs steps 1 to 10: it prints the description of each step and each
  command, waits until you press Enter, and runs the command. When a command shows the result of
  the controller's work, the script first waits until that work is done.
  `NONINTERACTIVE=1 hack/demo.sh` (or `make demo-check KIND_CLUSTER=namespaceclass-demo`) runs the
  steps without pauses. The script refuses to run unless the current context of `KUBECONFIG` is a
  `kind-*` context.

The output below comes from a real run of the script (see
[How this guide is checked](#how-this-guide-is-checked)). Lines that start with `$` are commands;
the other lines are their output. Names of pods, UIDs, resource versions, timestamps and ages differ
in every run. The script ran without pauses, so most ages are a few seconds.

The controller works in the background, usually within a second. If you type the commands and a
result is not there yet, run the command again.

## Step 0: Set up

```sh
make kind-deploy KIND_CLUSTER=namespaceclass-demo
export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig
kubectl config current-context
kubectl get pods -n namespaceclass-system
```

`make kind-deploy` creates the kind cluster `namespaceclass-demo` (unless it exists), builds the
controller image, loads it into the cluster, installs the CRD and deploys the controller. The first
run takes a few minutes because it builds the image.

Expected output of the last two commands:

```console
$ kubectl config current-context
kind-namespaceclass-demo
$ kubectl get pods -n namespaceclass-system
NAME                                                 READY   STATUS    RESTARTS   AGE
namespaceclass-controller-manager-7c6899b876-wr9kb   1/1     Running   0          15s
```

If the pod is not `Running` yet, run the last command again after a few seconds.

What to point out:

- One command sets up everything.
- `bin/kind-namespaceclass-demo.kubeconfig` holds only the kind cluster. After the `export`, every
  command in this shell goes to the demo cluster.

To run steps 1 to 10 with the script, run `hack/demo.sh` now.

## Step 1: Create the classes

```console
$ cat config/samples/namespaceclass_v1alpha1_public-network.yaml
apiVersion: namespaceclass.akuity.io/v1alpha1
kind: NamespaceClass
metadata:
  name: public-network
spec:
  resources:
    - apiVersion: networking.k8s.io/v1
      kind: NetworkPolicy
      metadata:
        name: ingress
      spec:
        podSelector: {}
        policyTypes:
          - Ingress
        ingress:
          - from:
              - ipBlock:
                  cidr: 0.0.0.0/0

$ kubectl apply -f config/samples/namespaceclass_v1alpha1_public-network.yaml -f config/samples/namespaceclass_v1alpha1_internal-network.yaml
namespaceclass.namespaceclass.akuity.io/public-network created
namespaceclass.namespaceclass.akuity.io/internal-network created

$ kubectl get nsclass
NAME               AGE
internal-network   0s
public-network     0s
```

What to point out:

- A class is a list of normal manifests. Any namespaced kind works, including custom resources.
- public-network has one NetworkPolicy that allows ingress from any address.
- internal-network
  ([file](../config/samples/namespaceclass_v1alpha1_internal-network.yaml)) has three items: the
  NetworkPolicy `ingress` (ingress only from the VPN range `10.8.0.0/16`), the NetworkPolicy
  `egress` (egress only to the VPN range and to DNS) and the ConfigMap `network-info`.
- `nsclass` is the short name of `namespaceclass`.

## Step 2: Use a class

```console
$ kubectl apply -f config/samples/namespace_web-portal.yaml
namespace/web-portal created

$ kubectl get networkpolicy -n web-portal
NAME      POD-SELECTOR   AGE
ingress   <none>         0s

$ kubectl get networkpolicy ingress -n web-portal -o yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  creationTimestamp: "2026-10-04T20:04:05Z"
  generation: 1
  labels:
    namespaceclass.akuity.io/class: public-network
  name: ingress
  namespace: web-portal
  ownerReferences:
  - apiVersion: namespaceclass.akuity.io/v1alpha1
    controller: true
    kind: NamespaceClass
    name: public-network
    uid: 132c4c91-733d-4819-8cd5-dada4a0b5bf5
  resourceVersion: "575"
  uid: 8fb64725-8dcd-4cd2-82b0-14336b3507a5
spec:
  ingress:
  - from:
    - ipBlock:
        cidr: 0.0.0.0/0
  podSelector: {}
  policyTypes:
  - Ingress

$ kubectl get namespace web-portal -o jsonpath='{.metadata.annotations.namespaceclass\.akuity\.io/managed-resources}'
[{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]

$ kubectl events -n default --for namespace/web-portal
LAST SEEN   TYPE     REASON    OBJECT                 MESSAGE
0s          Normal   Created   Namespace/web-portal   created NetworkPolicy.networking.k8s.io/ingress
```

What to point out:

- The namespace has the label `namespaceclass.akuity.io/name: public-network`
  ([file](../config/samples/namespace_web-portal.yaml)).
- The created NetworkPolicy has the label `namespaceclass.akuity.io/class: public-network` and an
  owner reference to the class (`controller: true`). The label marks it as created by the
  controller. The owner reference lets Kubernetes delete it with the class (step 10).
- The annotation `namespaceclass.akuity.io/managed-resources` on the namespace lists what the
  controller created there. The controller uses it to find what to delete later.
- Events about a namespace are stored in the `default` namespace, because a Namespace is a
  cluster-scoped object.

## Step 3: Switch to another class

```console
$ kubectl get networkpolicy ingress -n web-portal -o jsonpath='{.metadata.uid}'
8fb64725-8dcd-4cd2-82b0-14336b3507a5

$ kubectl label namespace web-portal namespaceclass.akuity.io/name=internal-network --overwrite
namespace/web-portal labeled

$ kubectl get networkpolicy,configmap -n web-portal -L namespaceclass.akuity.io/class
NAME                                      POD-SELECTOR   AGE   CLASS
networkpolicy.networking.k8s.io/egress    <none>         0s    internal-network
networkpolicy.networking.k8s.io/ingress   <none>         0s    internal-network

NAME                         DATA   AGE   CLASS
configmap/kube-root-ca.crt   1      0s
configmap/network-info       1      0s    internal-network

$ kubectl get networkpolicy ingress -n web-portal -o jsonpath='{.metadata.uid} {.spec.ingress[0].from[0].ipBlock.cidr}'
8fb64725-8dcd-4cd2-82b0-14336b3507a5 10.8.0.0/16
```

What to point out:

- Both classes define the NetworkPolicy `ingress`. The controller updated it in place: the UID is
  the same as before, the allowed range is now the VPN range, and the class label and owner
  reference now name internal-network. Nothing was deleted and created again.
- `egress` and `network-info` are new. If public-network had objects that internal-network does
  not have, the controller would delete them after applying internal-network.
- `kube-root-ca.crt` has no class label. Kubernetes creates it in every namespace, and the
  controller leaves it alone.

## Step 4: Update a class

[`docs/demo/internal-network-v2.yaml`](demo/internal-network-v2.yaml) is version 2 of
internal-network. It changes the VPN range to `10.9.0.0/16` and adds the NetworkPolicy
`allow-monitoring`.

```console
$ kubectl apply -f config/samples/namespace_billing.yaml
namespace/billing created

$ kubectl apply -f docs/demo/internal-network-v2.yaml
namespaceclass.namespaceclass.akuity.io/internal-network configured

$ kubectl get networkpolicy -A -l namespaceclass.akuity.io/class=internal-network
NAMESPACE    NAME               POD-SELECTOR   AGE
billing      allow-monitoring   <none>         0s
billing      egress             <none>         1s
billing      ingress            <none>         1s
web-portal   allow-monitoring   <none>         0s
web-portal   egress             <none>         1s
web-portal   ingress            <none>         1s

$ kubectl get networkpolicy ingress -n billing -o jsonpath='{.spec.ingress[0].from[0].ipBlock.cidr}'
10.9.0.0/16

$ kubectl apply -f config/samples/namespaceclass_v1alpha1_internal-network.yaml
namespaceclass.namespaceclass.akuity.io/internal-network configured

$ kubectl get networkpolicy -A -l namespaceclass.akuity.io/class=internal-network
NAMESPACE    NAME      POD-SELECTOR   AGE
billing      egress    <none>         1s
billing      ingress   <none>         1s
web-portal   egress    <none>         1s
web-portal   ingress   <none>         1s
```

What to point out:

- billing also uses internal-network. Every namespace of a class follows changes to the class:
  both namespaces got `allow-monitoring` and the new range.
- Applying the original class again deleted `allow-monitoring` in both namespaces and put the old
  range back.

## Step 5: Undo changes made by hand

```console
$ kubectl delete networkpolicy egress -n web-portal
networkpolicy.networking.k8s.io "egress" deleted from web-portal namespace

$ kubectl get networkpolicy egress -n web-portal
NAME     POD-SELECTOR   AGE
egress   <none>         0s

$ kubectl patch networkpolicy ingress -n web-portal --type=merge -p '{"spec":{"ingress":[]}}'
networkpolicy.networking.k8s.io/ingress patched

$ kubectl get networkpolicy ingress -n web-portal -o jsonpath='{.spec.ingress[0].from[0].ipBlock.cidr}'
10.8.0.0/16
```

What to point out:

- The controller watches the objects it created. The deleted NetworkPolicy is back (its age starts
  again from zero), and the emptied ingress rule is back.
- Only fields that the class sets are put back. Fields that the class does not set, for example a
  label you add to the object, are left alone.

## Step 6: Leave existing objects alone

```console
$ kubectl create namespace legacy
namespace/legacy created

$ kubectl create configmap network-info -n legacy --from-literal=owner=me
configmap/network-info created

$ kubectl label namespace legacy namespaceclass.akuity.io/name=internal-network
namespace/legacy labeled

$ kubectl events -n default --for namespace/legacy
LAST SEEN   TYPE      REASON     OBJECT             MESSAGE
0s          Warning   Conflict   Namespace/legacy   ConfigMap/network-info already exists and was not created by namespaceclass-controller; it is left unchanged
0s          Normal    Created    Namespace/legacy   created NetworkPolicy.networking.k8s.io/ingress
0s          Normal    Created    Namespace/legacy   created NetworkPolicy.networking.k8s.io/egress

$ kubectl get configmap network-info -n legacy -o yaml
apiVersion: v1
data:
  owner: me
kind: ConfigMap
metadata:
  creationTimestamp: "2026-10-04T20:04:06Z"
  name: network-info
  namespace: legacy
  resourceVersion: "647"
  uid: 8dc643ce-ed8e-4def-9c6b-0a9bbcfa5c7d

$ kubectl get namespace legacy -o jsonpath='{.metadata.annotations.namespaceclass\.akuity\.io/managed-resources}'
[{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"egress"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]
```

What to point out:

- The ConfigMap `network-info` existed before and the controller did not create it. The controller
  left it unchanged (no class label, no owner reference, `owner: me`) and recorded a `Conflict`
  warning.
- The other objects of the class were created as usual.
- The annotation lists only the two NetworkPolicies, so the controller will never delete the
  ConfigMap, also not when the namespace opts out.
- A conflict is not an error and is not retried. The controller checks the name again the next
  time it processes the namespace for another reason.

## Step 7: Any kind of object

```console
$ kubectl apply -f config/samples/namespaceclass_v1alpha1_team-baseline.yaml
namespaceclass.namespaceclass.akuity.io/team-baseline created

$ kubectl apply -f config/samples/namespace_team-a.yaml
namespace/team-a created

$ kubectl get serviceaccount,resourcequota,limitrange,rolebinding -n team-a -l namespaceclass.akuity.io/class=team-baseline
NAME                      AGE
serviceaccount/deployer   0s

NAME                    REQUEST                                                 LIMIT                                    AGE
resourcequota/compute   pods: 0/20, requests.cpu: 0/4, requests.memory: 0/8Gi   limits.cpu: 0/8, limits.memory: 0/16Gi   0s

NAME                  CREATED AT
limitrange/defaults   2026-10-04T20:04:07Z

NAME                                                  ROLE               AGE
rolebinding.rbac.authorization.k8s.io/deployer-view   ClusterRole/view   0s
```

What to point out:

- One class ([file](../config/samples/namespaceclass_v1alpha1_team-baseline.yaml)) created a
  ServiceAccount, a ResourceQuota, a LimitRange and a RoleBinding.
- The subject of the RoleBinding has no namespace, so it means the ServiceAccount `deployer` in the
  RoleBinding's own namespace. This is how one class works in every namespace without templating.

## Step 8: A typo in the label

```console
$ kubectl label namespace team-a namespaceclass.akuity.io/name=team-baselin --overwrite
namespace/team-a labeled

$ kubectl events -n default --for namespace/team-a
LAST SEEN   TYPE      REASON          OBJECT             MESSAGE
0s          Normal    Created         Namespace/team-a   created ServiceAccount/deployer
0s          Normal    Created         Namespace/team-a   created ResourceQuota/compute
0s          Normal    Created         Namespace/team-a   created LimitRange/defaults
0s          Normal    Created         Namespace/team-a   created RoleBinding.rbac.authorization.k8s.io/deployer-view
0s          Warning   ClassNotFound   Namespace/team-a   NamespaceClass "team-baselin" does not exist; nothing was changed

$ kubectl get serviceaccount,resourcequota,limitrange,rolebinding -n team-a -l namespaceclass.akuity.io/class=team-baseline
NAME                      AGE
serviceaccount/deployer   0s

NAME                    REQUEST                                                 LIMIT                                    AGE
resourcequota/compute   pods: 0/20, requests.cpu: 0/4, requests.memory: 0/8Gi   limits.cpu: 0/8, limits.memory: 0/16Gi   0s

NAME                  CREATED AT
limitrange/defaults   2026-10-04T20:04:07Z

NAME                                                  ROLE               AGE
rolebinding.rbac.authorization.k8s.io/deployer-view   ClusterRole/view   0s

$ kubectl label namespace team-a namespaceclass.akuity.io/name=team-baseline --overwrite
namespace/team-a labeled
```

What to point out:

- A label that names a class that does not exist changes nothing: a `ClassNotFound` warning, and
  all four objects are still there. Only a missing label means "opt out", so a typo cannot delete
  the objects of a namespace.
- After the label is fixed, there is nothing to do: the objects already match the class.

## Step 9: Opt out

```console
$ kubectl label namespace web-portal namespaceclass.akuity.io/name-
namespace/web-portal unlabeled

$ kubectl get networkpolicy,configmap -n web-portal
NAME                         DATA   AGE
configmap/kube-root-ca.crt   1      2s

$ kubectl get namespace web-portal -o jsonpath='{.metadata.annotations.namespaceclass\.akuity\.io/managed-resources}'

```

What to point out:

- Removing the label deleted every object the controller created in web-portal. `kube-root-ca.crt`
  stays, because the controller did not create it.
- The last command prints nothing: the annotation was removed too.

## Step 10: Delete a class

```console
$ kubectl get networkpolicy,configmap -A -l namespaceclass.akuity.io/class=internal-network
NAMESPACE   NAME                                      POD-SELECTOR   AGE
billing     networkpolicy.networking.k8s.io/egress    <none>         2s
billing     networkpolicy.networking.k8s.io/ingress   <none>         2s
legacy      networkpolicy.networking.k8s.io/egress    <none>         0s
legacy      networkpolicy.networking.k8s.io/ingress   <none>         0s

NAMESPACE   NAME                     DATA   AGE
billing     configmap/network-info   1      2s

$ kubectl delete namespaceclass internal-network
namespaceclass.namespaceclass.akuity.io "internal-network" deleted

$ kubectl get networkpolicy,configmap -A -l namespaceclass.akuity.io/class=internal-network
No resources found

$ kubectl get configmap network-info -n legacy
NAME           DATA   AGE
network-info   1      3s

$ kubectl events -n default --for namespace/legacy
LAST SEEN         TYPE      REASON          OBJECT             MESSAGE
2s                Warning   Conflict        Namespace/legacy   ConfigMap/network-info already exists and was not created by namespaceclass-controller; it is left unchanged
2s                Normal    Created         Namespace/legacy   created NetworkPolicy.networking.k8s.io/ingress
2s                Normal    Created         Namespace/legacy   created NetworkPolicy.networking.k8s.io/egress
1s (x2 over 1s)   Warning   ClassNotFound   Namespace/legacy   NamespaceClass "internal-network" does not exist; nothing was changed
```

What to point out:

- The Kubernetes garbage collector deleted the objects of the class in billing and legacy, because
  each one has an owner reference to the class. The controller did not delete them.
- The ConfigMap in legacy that you created in step 6 is still there: it has no owner reference.
- billing and legacy are still labeled with the deleted class, so they get a `ClassNotFound`
  warning and nothing else changes. `(x2 over 1s)` means the same event happened twice and was
  merged: the garbage collector's deletions started one more run of the controller.
- `kubectl delete namespaceclass internal-network --cascade=orphan` would keep the objects.
- The first class deletion after the CRD is installed can take up to about 30 seconds, because the
  garbage collector must first discover the new CRD. If the objects are still there, wait and run
  the command again.

## Step 11: Clean up

```sh
make cleanup-test-e2e KIND_CLUSTER=namespaceclass-demo
unset KUBECONFIG
```

Expected output of the first command:

```console
$ make cleanup-test-e2e KIND_CLUSTER=namespaceclass-demo
Deleting cluster "namespaceclass-demo" ...
Deleted nodes: ["namespaceclass-demo-control-plane"]
```

## Run the demo again on the same cluster

Instead of step 11, you can delete the objects of the earlier run and run the demo again.
`hack/demo.sh` refuses to start while they exist. These commands name the kubeconfig file of the
kind cluster, so they work in any shell started in the repository root, also after
`unset KUBECONFIG`:

```sh
KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete namespace web-portal billing team-a legacy --ignore-not-found
KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete namespaceclass public-network internal-network team-baseline --ignore-not-found
```

## How this guide is checked

An e2e spec ([`test/e2e/demo_test.go`](../test/e2e/demo_test.go), Ginkgo label `demo`) runs
`hack/demo.sh` with `NONINTERACTIVE=1` on the kind cluster `namespaceclass-demo` (created if it
does not exist), where the controller is deployed, and saves the output to `bin/demo-output.txt`.
The output in this guide is copied from that file.

```sh
make test-e2e KIND_CLUSTER=namespaceclass-demo E2E_LABEL_FILTER=demo
```

The target creates the cluster, deploys the controller, runs only the demo spec, and deletes the
cluster at the end. Run it when no kind cluster with that name exists (for example after step 11):
it removes the controller from the cluster when it finishes. Plain `make test-e2e` skips this spec.
