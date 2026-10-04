# 10: An object with the same name already exists

Sometimes an object with the kind and name of a class item already exists in the namespace, and the
controller did not create it: it has neither the class label nor an owner reference to a
NamespaceClass. The controller leaves that object unchanged and records a `Conflict` warning. It
applies the other objects as usual and never lists the existing object in the annotation, so it
never deletes it. A conflict is not an error and is not retried. The controller checks the name
again the next time it processes the namespace for another reason.

This page follows [step 6 of the demo](../demo.md#step-6-leave-existing-objects-alone).

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

The namespace legacy has no class label. A user created the ConfigMap `network-info` in it by hand.
internal-network ([sample](../../config/samples/namespaceclass_v1alpha1_internal-network.yaml))
also has a ConfigMap named `network-info`.

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl create namespace legacy
namespace/legacy created

$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl create configmap network-info -n legacy --from-literal=owner=me
configmap/network-info created
```

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: network-info
  namespace: legacy
  uid: 8dc643ce-ed8e-4def-9c6b-0a9bbcfa5c7d
  resourceVersion: "647"
data:
  owner: me
```

## Action

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl label namespace legacy namespaceclass.akuity.io/name=internal-network
namespace/legacy labeled
```

## What starts a run

The Namespace watch sees an update event. It passes the filter because the label was added.

## The run, step by step

1. **Read the namespace**: legacy has the label `namespaceclass.akuity.io/name: internal-network`
   and no annotation.
2. **Previous list**: previous = `{}`.
3. **Desired objects**, in the order of the class: `NetworkPolicy.networking.k8s.io/ingress`,
   `NetworkPolicy.networking.k8s.io/egress`, `ConfigMap/network-info`.
4. **Drift watches**: NetworkPolicy and ConfigMap already have ready watches.
5. **Live read and classify**:
   - `ingress` and `egress` do not exist, so they are new.
   - `network-info` exists. It has no label `namespaceclass.akuity.io/class` and no owner reference
     to a NamespaceClass, so it is not managed. Warning `Conflict`, recorded now, before anything is
     written.

   conflicts = `{ConfigMap/network-info}`.
6. **Record the old and the new objects**: previous ∪ (desired − conflicts) =
   `{NetworkPolicy.networking.k8s.io/egress, NetworkPolicy.networking.k8s.io/ingress}`. The controller
   writes
   `[{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"egress"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]`
7. **Apply**: `ingress` and `egress` are created (`Created` for each). `network-info` is not applied.
8. **Delete old objects**: a conflict is not a failure, so this step runs. previous − desired =
   `{}`, nothing to delete.
9. **Record the current objects**: (desired − conflicts) ∪ failed deletions is the list of step 6.
   Nothing is written.
10. **Result**: done. The run returns no error, so the conflict is not retried.

## Resulting state

Real output from the demo:

```console
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

What changed:

- The user's ConfigMap `network-info` did not change: same uid, same resourceVersion `"647"`, no
  class label, no owner reference, data `owner: me`.
- The NetworkPolicies `ingress` and `egress` are new, with the label and owner reference of
  internal-network.
- The annotation lists only the two NetworkPolicies. When legacy opts out, the controller deletes
  them and leaves the ConfigMap alone.

## Events

Real output from the demo:

```console
$ kubectl events -n default --for namespace/legacy
LAST SEEN   TYPE      REASON     OBJECT             MESSAGE
0s          Warning   Conflict   Namespace/legacy   ConfigMap/network-info already exists and was not created by namespaceclass-controller; it is left unchanged
0s          Normal    Created    Namespace/legacy   created NetworkPolicy.networking.k8s.io/ingress
0s          Normal    Created    Namespace/legacy   created NetworkPolicy.networking.k8s.io/egress
```

## A conflict during a switch

A conflict does not stop the deletion of the old class's objects. Suppose legacy used team-baseline
before its label was changed to internal-network. Then:

- previous = the four objects of team-baseline; conflicts = `{ConfigMap/network-info}`.
- Step 6 writes the four team-baseline objects and the two NetworkPolicies.
- Step 7 creates `ingress` and `egress`.
- No item failed, so step 8 deletes the four team-baseline objects.
- Step 9 writes the list of the two NetworkPolicies.

## Later runs while the conflict lasts

Any later run for legacy reads `network-info` again. If it still is not managed, the run records
the `Conflict` warning again and writes nothing, because the annotation already holds the right
list. If neither legacy nor the ConfigMap changed since the last `Conflict` event, and that event is
recent, the new warning is merged into it with a count (for example `x2`). A run happens after a
class edit, a label change, drift of one of the NetworkPolicies, a restart of the controller, or the
periodic resync (about every 10 hours).

## When the user deletes their object

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete configmap network-info -n legacy
configmap "network-info" deleted from legacy namespace
```

This starts no run: the drift watches only see objects with the class label, and the namespace did
not change. The class's ConfigMap is created by the next run for legacy that happens for one of the
reasons above. In that run, `ConfigMap/network-info` does not exist, so it is new. Step 6 adds it to
the annotation:
`[{"kind":"ConfigMap","name":"network-info"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"egress"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]`.
Step 7 creates it with the class label and owner reference.

```console
$ kubectl events -n default --for namespace/legacy
LAST SEEN   TYPE     REASON    OBJECT             MESSAGE
0s          Normal   Created   Namespace/legacy   created ConfigMap/network-info
```
