# 04: Removing the label (opt out)

When the label `namespaceclass.akuity.io/name` is removed from a namespace, the namespace wants no
objects. The controller deletes every object that the annotation lists and that it still manages,
and then removes the annotation. Objects that the controller did not create stay.

This page follows [step 9 of the demo](../demo.md#step-9-opt-out).

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

web-portal uses internal-network
([sample](../../config/samples/namespaceclass_v1alpha1_internal-network.yaml)), as after step 3 of
the demo.

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: web-portal
  labels:
    namespaceclass.akuity.io/name: internal-network
  annotations:
    namespaceclass.akuity.io/managed-resources: '[{"kind":"ConfigMap","name":"network-info"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"egress"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]'
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: network-info
  namespace: web-portal
  labels:
    namespaceclass.akuity.io/class: internal-network
  ownerReferences:
    - apiVersion: namespaceclass.akuity.io/v1alpha1
      kind: NamespaceClass
      name: internal-network
      uid: 1dc1b74b-c9bd-4ae0-a39e-533b4f3754c5
      controller: true
data:
  vpnCIDR: 10.8.0.0/16
```

The NetworkPolicies `ingress` and `egress` have the same label and owner reference. The namespace
also has the ConfigMap `kube-root-ca.crt`, which Kubernetes creates in every namespace. It has no
class label and is not in the annotation.

## Action

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl label namespace web-portal namespaceclass.akuity.io/name-
namespace/web-portal unlabeled
```

## What starts a run

The Namespace watch sees an update event. It passes the filter because the label was removed: the
old namespace has it, the new one does not.

## The run, step by step

1. **Read the namespace**: web-portal has no class label. The annotation lists three objects.
2. **Previous list**: previous = `{ConfigMap/network-info, NetworkPolicy.networking.k8s.io/egress,
   NetworkPolicy.networking.k8s.io/ingress}`.
3. **Desired objects**: there is no label, so desired = `{}`. This is how opting out works: the same
   run as always, with nothing desired.
4. **Drift watches**: no desired objects, so nothing to do.
5. **Live read and classify**: nothing to read. conflicts = `{}`.
6. **Record the old and the new objects**: previous ∪ `{}` = previous. The annotation already holds
   this text, so nothing is written.
7. **Apply**: nothing to apply.
8. **Delete old objects**: no item failed, so the controller deletes previous − desired = all three,
   in this order: `ConfigMap/network-info`, `NetworkPolicy.networking.k8s.io/egress`,
   `NetworkPolicy.networking.k8s.io/ingress`. For each one it reads the object, sees that it is
   still managed, and deletes it with a precondition on its UID and resourceVersion and with
   background propagation. Event `Deleted` for each.
9. **Record the current objects**: (desired − conflicts) ∪ failed deletions = `{}`. An empty list
   means the controller removes the annotation.
10. **Result**: done.

Cases that the delete step handles without an error:

- An object that is already gone, or whose kind no longer exists, counts as deleted. There is no
  event.
- An object that is no longer managed (both its class label and its owner reference were removed,
  see [use case 12](12-label-removed-from-object.md)) is left alone and dropped from the list.

## Resulting state

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: web-portal
```

What changed:

- The ConfigMap `network-info` and the NetworkPolicies `egress` and `ingress` are deleted.
- The annotation is removed. web-portal now looks like any namespace that never used a class.
- `kube-root-ca.crt` is still there: the controller did not create it.

Real output from the demo:

```console
$ kubectl get networkpolicy,configmap -n web-portal
NAME                         DATA   AGE
configmap/kube-root-ca.crt   1      2s

$ kubectl get namespace web-portal -o jsonpath='{.metadata.annotations.namespaceclass\.akuity\.io/managed-resources}'

```

An object that the controller did not create is never in the annotation. For example, when legacy
opts out ([use case 10](10-existing-object.md)), the controller deletes its two NetworkPolicies and
leaves the user's ConfigMap `network-info` alone.

## Events

```console
$ kubectl events -n default --for namespace/web-portal
LAST SEEN   TYPE     REASON    OBJECT                 MESSAGE
0s          Normal   Deleted   Namespace/web-portal   deleted ConfigMap/network-info
0s          Normal   Deleted   Namespace/web-portal   deleted NetworkPolicy.networking.k8s.io/egress
0s          Normal   Deleted   Namespace/web-portal   deleted NetworkPolicy.networking.k8s.io/ingress
```
