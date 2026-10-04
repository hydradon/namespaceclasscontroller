# Manual testing

This page shows how to run the controller on a local kind cluster and try it with kubectl, one
command at a time. It lists the commands and what you should see, without their output. For a guided
walkthrough with the real output of each command, see the [demo guide](demo.md).

> Run the commands on this page only in the kind cluster `namespaceclass-demo`. Create it as in
> [step 1](#1-create-the-cluster-and-start-the-controller), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> `kubectl delete` and `kubectl scale` command also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the kind cluster even
> without the `export`.

## Before you start

You need Go, Docker and kubectl 1.26 or newer (this page uses `kubectl events`). Docker must be
running. The Makefile installs kind and the other tools into `bin/`. Run every command in the
repository root.

**Use the kubeconfig file of the kind cluster in every terminal.** The kind targets
(`make kind-deploy`, `make setup-test-e2e` and `make cleanup-test-e2e`) always use this file. But
`make install`, `make deploy`, `make run` and `make undeploy` use whatever `KUBECONFIG` points to.
When `KUBECONFIG` is not set, they use the current context of `~/.kube/config`, which can be a real
cluster. So in every terminal, run `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`
and check that `kubectl config current-context` prints `kind-namespaceclass-demo`.

The cluster name `namespaceclass-demo` keeps this cluster separate from `make test-e2e`, which
creates its own cluster `namespaceclass-test-e2e` and deletes it at the end. Do not run
`make test-e2e` with `KIND_CLUSTER=namespaceclass-demo` while you use this cluster: at the end, it
removes the controller and the CRD from that cluster, and it deletes the cluster when all specs
pass.

## 1. Create the cluster and start the controller

There are two ways to run the controller. Use one of them, not both at the same time (see
[Switch between the options](#switch-between-the-options)).

### Option A: the controller runs in the cluster

This is the closest to a real installation.

```sh
make kind-deploy KIND_CLUSTER=namespaceclass-demo
export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig
kubectl config current-context        # kind-namespaceclass-demo
kubectl rollout status deployment/namespaceclass-controller-manager -n namespaceclass-system
git restore config/manager/kustomization.yaml   # make deploy wrote the image name into this file
```

`make kind-deploy` does these steps:

1. Creates the kind cluster `namespaceclass-demo` unless it exists, and writes its kubeconfig file
   to `bin/kind-namespaceclass-demo.kubeconfig`.
2. Builds the controller image `namespaceclass-controller:dev`.
3. Loads the image into the cluster.
4. Installs the CRD and deploys the controller into the namespace `namespaceclass-system`.

The first run takes a few minutes because it builds the image. `kubectl rollout status` waits until
the controller pod is ready.

To follow the logs of the controller, open a second terminal in the repository root, run the same
`export`, and run:

```sh
kubectl logs -n namespaceclass-system deploy/namespaceclass-controller-manager -f
```

### Option B: the controller runs on your machine

This is faster while you change the code, and the logs appear in your terminal.

```sh
make setup-test-e2e KIND_CLUSTER=namespaceclass-demo   # only creates the cluster
export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig
kubectl config current-context                         # kind-namespaceclass-demo
make install                                           # installs the CRD
make run                                               # runs the controller here; Ctrl+C stops it
```

`make setup-test-e2e` creates the kind cluster unless it exists, and writes its kubeconfig file. It
does not build or deploy the controller. `make run` keeps running, so run the commands of the next
steps in a second terminal, after the same `export`.

`make run` starts the controller with the default flags of [`cmd/main.go`](../cmd/main.go):

- Leader election is off. The controller does not check whether another copy of it runs, for example
  the one in the cluster.
- The health probes listen on port 8081 of your machine. If another program uses this port, the
  controller stops with `Failed to start manager` and `address already in use`. Stop that program,
  or run `go run ./cmd/main.go --health-probe-bind-address=:8082` instead of `make run`.
- The metrics endpoint is off.

### Switch between the options

Do not run both options at the same time: two controllers would write the same objects. To switch
from option A to option B, stop the controller in the cluster:

```sh
KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl scale deployment/namespaceclass-controller-manager -n namespaceclass-system --replicas=0
```

To switch back, stop `make run` with Ctrl+C and run the same command with `--replicas=1`.

Do not use `make undeploy` to stop the controller in the cluster. It also deletes the CRD, which
deletes every NamespaceClass, and the garbage collector then deletes every object the controller
created.

## 2. Apply classes and namespaces

The samples in [`config/samples/`](../config/samples/) define three classes, and one namespace for
each class:

| Class | Objects of the class | Sample namespace |
|---|---|---|
| [public-network](../config/samples/namespaceclass_v1alpha1_public-network.yaml) | NetworkPolicy `ingress` (ingress from any address) | web-portal |
| [internal-network](../config/samples/namespaceclass_v1alpha1_internal-network.yaml) | NetworkPolicy `ingress` (ingress only from the VPN range `10.8.0.0/16`), NetworkPolicy `egress` (egress only to the VPN range and to DNS), ConfigMap `network-info` | billing |
| [team-baseline](../config/samples/namespaceclass_v1alpha1_team-baseline.yaml) | ServiceAccount `deployer`, ResourceQuota `compute`, LimitRange `defaults`, RoleBinding `deployer-view` | team-a |

Apply the classes, one file at a time:

```sh
kubectl apply -f config/samples/namespaceclass_v1alpha1_public-network.yaml
kubectl apply -f config/samples/namespaceclass_v1alpha1_internal-network.yaml
kubectl apply -f config/samples/namespaceclass_v1alpha1_team-baseline.yaml
kubectl get nsclass
kubectl get nsclass public-network -o yaml
```

Then apply the namespaces. Each one has the label `namespaceclass.akuity.io/name`, which names its
class:

```sh
kubectl apply -f config/samples/namespace_web-portal.yaml   # uses public-network
kubectl apply -f config/samples/namespace_billing.yaml      # uses internal-network
kubectl apply -f config/samples/namespace_team-a.yaml       # uses team-baseline
```

To try a namespace of your own, create it and add the label:

```sh
kubectl create namespace test1
kubectl label namespace test1 namespaceclass.akuity.io/name=internal-network
```

Notes:

- Apply a class before the namespaces that use it. The other order works too, but the namespace
  first gets a `ClassNotFound` warning. Its objects are created once the class exists.
- `kubectl apply -k config/samples` applies the three classes in one command.
- Do not use `kubectl apply -f config/samples/`. kubectl then reads every file in the folder,
  including `kustomization.yaml`, which is not a Kubernetes object, and it applies the files in name
  order, so the namespaces come before their classes.

## 3. Look at what the controller did

The controller works in the background, usually within a second. If a result is not there yet, run
the command again.

| To see | Command |
|---|---|
| The NetworkPolicies and ConfigMaps in a namespace | `kubectl get networkpolicy,configmap -n web-portal` |
| One object in full, with the label `namespaceclass.akuity.io/class` and the owner reference to its class | `kubectl get networkpolicy ingress -n web-portal -o yaml` |
| Which field manager owns which field of the object | `kubectl get networkpolicy ingress -n web-portal -o yaml --show-managed-fields` |
| The NetworkPolicies and ConfigMaps that the controller created, in every namespace | `kubectl get networkpolicy,configmap -A -l namespaceclass.akuity.io/class` |
| The NetworkPolicies and ConfigMaps of one class | `kubectl get networkpolicy,configmap -A -l namespaceclass.akuity.io/class=internal-network` |
| The class of each namespace | `kubectl get namespaces -L namespaceclass.akuity.io/name` |
| The list of objects that the controller created in a namespace (the annotation `namespaceclass.akuity.io/managed-resources`) | `kubectl get namespace web-portal -o jsonpath='{.metadata.annotations.namespaceclass\.akuity\.io/managed-resources}'; echo` |
| The events about a namespace | `kubectl events -n default --for namespace/web-portal` |
| The objects of team-baseline in team-a | `kubectl get serviceaccount,resourcequota,limitrange,rolebinding -n team-a -l namespaceclass.akuity.io/class=team-baseline` |

Notes:

- Kubernetes creates the ConfigMap `kube-root-ca.crt` in every namespace. The controller leaves it
  alone.
- The field manager of the controller is `namespaceclass-controller`.
- Events about a namespace are stored in the `default` namespace, because a Namespace is a
  cluster-scoped object.

## 4. Things to try

Try these in order, because each one starts where the one before it ends. The first one starts from
the end of step 2: web-portal uses public-network, and billing (and test1, if you created it) uses
internal-network. Each part has the commands, what you should see, and a link to the use-case page
that explains each step of the controller's run.

### Switch to another class

```sh
kubectl get networkpolicy ingress -n web-portal -o jsonpath='{.metadata.uid}'; echo
kubectl label namespace web-portal namespaceclass.akuity.io/name=internal-network --overwrite
kubectl get networkpolicy ingress -n web-portal -o jsonpath='{.metadata.uid} {.spec.ingress[0].from[0].ipBlock.cidr}'; echo
kubectl get networkpolicy,configmap -n web-portal
```

What you should see: `ingress` keeps its UID and now allows only the VPN range `10.8.0.0/16`.
`egress` and `network-info` are created. See [use case 02](use-cases/02-switch-class.md).

### Edit a class

[`docs/demo/internal-network-v2.yaml`](demo/internal-network-v2.yaml) changes the VPN range of
internal-network to `10.9.0.0/16` and adds the NetworkPolicy `allow-monitoring`.

```sh
kubectl apply -f docs/demo/internal-network-v2.yaml
kubectl get networkpolicy -A -l namespaceclass.akuity.io/class=internal-network
kubectl get networkpolicy ingress -n billing -o jsonpath='{.spec.ingress[0].from[0].ipBlock.cidr}'; echo
kubectl apply -f config/samples/namespaceclass_v1alpha1_internal-network.yaml
kubectl get networkpolicy -A -l namespaceclass.akuity.io/class=internal-network
kubectl get networkpolicy ingress -n billing -o jsonpath='{.spec.ingress[0].from[0].ipBlock.cidr}'; echo
```

What you should see: every namespace that uses internal-network gets `allow-monitoring` and the
range `10.9.0.0/16`. After the second `apply`, `allow-monitoring` is deleted and the range is
`10.8.0.0/16` again. See [use case 03](use-cases/03-edit-class.md).

### Delete or edit a created object

```sh
KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete networkpolicy egress -n web-portal
kubectl get networkpolicy egress -n web-portal
kubectl patch networkpolicy ingress -n web-portal --type=merge -p '{"spec":{"ingress":[]}}'
kubectl get networkpolicy ingress -n web-portal -o jsonpath='{.spec.ingress[0].from[0].ipBlock.cidr}'; echo
```

What you should see: `egress` comes back. Its age starts again from zero, because it is a new object
with a new UID. The emptied ingress rule of `ingress` is put back, so the last command prints
`10.8.0.0/16`. See [use case 11](use-cases/11-drift.md).

### Add a field that the class does not set

```sh
kubectl label networkpolicy ingress -n web-portal team=a
kubectl get networkpolicy ingress -n web-portal --show-labels
```

What you should see: the label `team=a` stays. The controller puts back only the fields that the
class sets, and it records no event. See [use case 11](use-cases/11-drift.md).

### Remove the controller's label from an object

```sh
kubectl label networkpolicy ingress -n web-portal namespaceclass.akuity.io/class-
kubectl get networkpolicy ingress -n web-portal --show-labels
```

What you should see: the label `namespaceclass.akuity.io/class=internal-network` comes back, with
the event `Updated`. The owner reference to the class still marks the object as created by the
controller. See [use case 12](use-cases/12-label-removed-from-object.md).

### An object with the same name already exists

```sh
kubectl create namespace legacy
kubectl create configmap network-info -n legacy --from-literal=owner=me
kubectl label namespace legacy namespaceclass.akuity.io/name=internal-network
kubectl events -n default --for namespace/legacy
kubectl get configmap network-info -n legacy -o yaml
```

What you should see: a `Conflict` warning for `ConfigMap/network-info`. The ConfigMap is unchanged:
`owner: me`, no class label and no owner reference. The NetworkPolicies `ingress` and `egress` are
created as usual. See [use case 10](use-cases/10-existing-object.md).

### A typo in the label

```sh
kubectl label namespace web-portal namespaceclass.akuity.io/name=internal-netwrk --overwrite
kubectl events -n default --for namespace/web-portal
kubectl get networkpolicy,configmap -n web-portal
kubectl label namespace web-portal namespaceclass.akuity.io/name=internal-network --overwrite
```

What you should see: the warning `ClassNotFound` with the note
`NamespaceClass "internal-netwrk" does not exist; nothing was changed`, and all objects stay. Only a
missing label means opt out. After you fix the label, nothing changes, because the objects already
match the class. See [use case 06](use-cases/06-missing-class.md).

### Opt out

```sh
kubectl label namespace web-portal namespaceclass.akuity.io/name-
kubectl get networkpolicy,configmap -n web-portal
kubectl get namespace web-portal -o jsonpath='{.metadata.annotations.namespaceclass\.akuity\.io/managed-resources}'; echo
```

What you should see: the controller deletes `ingress`, `egress` and `network-info`, so only
`kube-root-ca.crt` is left. It also removes the annotation, so the last command prints an empty
line. See [use case 04](use-cases/04-opt-out.md).

### Delete a class

```sh
KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete nsclass internal-network
kubectl get networkpolicy,configmap -A -l namespaceclass.akuity.io/class=internal-network
kubectl get configmap network-info -n legacy
```

What you should see: the Kubernetes garbage collector deletes the objects of internal-network in
every namespace, because each one has an owner reference to the class. The second command prints
`No resources found`. The ConfigMap that you created in legacy stays, because it has no owner
reference. The namespaces that are still labeled with internal-network get a `ClassNotFound`
warning. The first class deletion after the CRD is installed can take up to about 30 seconds,
because the garbage collector must first discover the new CRD. If the objects are still there, wait
and run the command again. See [use case 05](use-cases/05-delete-class.md).

## 5. After you change the code

**Option A.** Build and load a new image, then restart the controller:

```sh
make kind-deploy KIND_CLUSTER=namespaceclass-demo
kubectl rollout restart deployment/namespaceclass-controller-manager -n namespaceclass-system
kubectl rollout status deployment/namespaceclass-controller-manager -n namespaceclass-system
git restore config/manager/kustomization.yaml
```

The image name stays `namespaceclass-controller:dev`, so `make kind-deploy` does not change the
Deployment, and Kubernetes does not start a new pod by itself. `kubectl rollout restart` starts a
new pod, which uses the newly loaded image. `make kind-deploy` also updates the CRD. If you changed
the API types in `api/v1alpha1/`, run `make generate` first, because `make kind-deploy` does not
regenerate `api/v1alpha1/zz_generated.deepcopy.go`.

**Option B.** Stop `make run` with Ctrl+C and run `make run` again. If you changed the API types in
`api/v1alpha1/`, run `make install` first to update the CRD in the cluster.

The unit tests and the envtest specs need no cluster: run `make test` (see
[Testing](../README.md#testing) in the README).

## 6. Clean up

To start over on the same cluster, delete the test namespaces and the classes, then continue with
[step 2](#2-apply-classes-and-namespaces):

```sh
KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete namespace web-portal billing team-a legacy test1 --ignore-not-found
KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete nsclass --all
```

When you are done, stop `make run` if it runs (option B), and delete the cluster:

```sh
make cleanup-test-e2e KIND_CLUSTER=namespaceclass-demo
unset KUBECONFIG
```
