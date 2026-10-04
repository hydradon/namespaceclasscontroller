# 02: Switching to another class

When the label of a namespace changes from one class to another, the controller first applies the
objects of the new class. Then it deletes the objects that only the old class had. An object that
both classes define (same group, kind and name) is updated in place: it keeps its UID, and its
class label and owner reference now name the new class.

This page starts where [step 3 of the demo](../demo.md#step-3-switch-to-another-class) ends:
web-portal uses internal-network. The page switches web-portal back to public-network.

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

Both classes define the NetworkPolicy `ingress`. Only internal-network
([sample](../../config/samples/namespaceclass_v1alpha1_internal-network.yaml)) defines the
NetworkPolicy `egress` and the ConfigMap `network-info`. public-network
([sample](../../config/samples/namespaceclass_v1alpha1_public-network.yaml)) has one item, the
NetworkPolicy `ingress` that allows ingress from `0.0.0.0/0`.

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
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: ingress
  namespace: web-portal
  uid: 8fb64725-8dcd-4cd2-82b0-14336b3507a5
  resourceVersion: "602"
  labels:
    namespaceclass.akuity.io/class: internal-network
  ownerReferences:
    - apiVersion: namespaceclass.akuity.io/v1alpha1
      kind: NamespaceClass
      name: internal-network
      uid: 1dc1b74b-c9bd-4ae0-a39e-533b4f3754c5
      controller: true
spec:
  podSelector: {}
  policyTypes:
    - Ingress
  ingress:
    - from:
        - ipBlock:
            cidr: 10.8.0.0/16
```

The NetworkPolicy `egress` and the ConfigMap `network-info` in web-portal have the same label and
owner reference (internal-network). Their content is the content of the class items.

## Action

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl label namespace web-portal namespaceclass.akuity.io/name=public-network --overwrite
namespace/web-portal labeled
```

## What starts a run

The Namespace watch sees an update event. It passes the filter because the value of the label
changed from internal-network to public-network.

## The run, step by step

1. **Read the namespace**: web-portal has the label `namespaceclass.akuity.io/name: public-network`.
   The annotation lists three objects.
2. **Previous list**: previous = `{ConfigMap/network-info, NetworkPolicy.networking.k8s.io/egress,
   NetworkPolicy.networking.k8s.io/ingress}`.
3. **Desired objects**: built from public-network. desired =
   `{NetworkPolicy.networking.k8s.io/ingress}`, with the cidr `0.0.0.0/0`, the label
   `namespaceclass.akuity.io/class: public-network` and an owner reference to public-network.
4. **Drift watches**: NetworkPolicy already has a drift watch, and it is ready.
5. **Live read and classify**: `ingress` exists. It has the label `namespaceclass.akuity.io/class`
   (value internal-network). Any value counts, so the object is managed and will be updated.
   conflicts = `{}`.
6. **Record the old and the new objects**: previous ∪ (desired − conflicts) = previous, because
   `ingress` is already listed. The annotation already holds this text, so nothing is written.
7. **Apply**: Server-Side Apply (with force) updates `ingress` in place. The cidr, the label value
   and the owner reference change, so the resourceVersion changes. Event `Updated`.
8. **Delete old objects**: every item was applied, so the controller deletes previous − desired =
   `{ConfigMap/network-info, NetworkPolicy.networking.k8s.io/egress}`, in this order. For each one
   it reads the object, sees that it is still managed, and deletes it with a precondition on its UID
   and resourceVersion. Event `Deleted` for each.
9. **Record the current objects**: (desired − conflicts) ∪ failed deletions =
   `{NetworkPolicy.networking.k8s.io/ingress}`. The controller writes
   `[{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]`.
10. **Result**: done.

The update and the two deletions also reach the NetworkPolicy and ConfigMap drift watches, so
web-portal is processed again. That run finds nothing to change and writes nothing.

## Resulting state

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: web-portal
  labels:
    namespaceclass.akuity.io/name: public-network
  annotations:
    namespaceclass.akuity.io/managed-resources: '[{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]'
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: ingress
  namespace: web-portal
  uid: 8fb64725-8dcd-4cd2-82b0-14336b3507a5
  resourceVersion: "731"
  labels:
    namespaceclass.akuity.io/class: public-network
  ownerReferences:
    - apiVersion: namespaceclass.akuity.io/v1alpha1
      kind: NamespaceClass
      name: public-network
      uid: 132c4c91-733d-4819-8cd5-dada4a0b5bf5
      controller: true
spec:
  podSelector: {}
  policyTypes:
    - Ingress
  ingress:
    - from:
        - ipBlock:
            cidr: 0.0.0.0/0
```

What changed:

- `ingress`: the uid is unchanged, so the object was not deleted and created again. The cidr changed
  from `10.8.0.0/16` to `0.0.0.0/0`. The label and the owner reference now point to public-network.
  The resourceVersion changed.
- `egress` and `network-info`: deleted, after `ingress` was applied.
- The annotation lists only `ingress`.

Server-Side Apply also removes a field that only the old class set, and keeps fields that other
field managers set, for example a label that a user added. Here both classes set the same fields of
`ingress`, so no field is removed.

In the other direction ([demo step 3](../demo.md#step-3-switch-to-another-class), public-network to
internal-network), `ingress` is updated in place in the same way, `egress` and `network-info` are
created, and nothing is deleted.

## Events

```console
$ kubectl events -n default --for namespace/web-portal
LAST SEEN   TYPE     REASON    OBJECT                 MESSAGE
0s          Normal   Updated   Namespace/web-portal   updated NetworkPolicy.networking.k8s.io/ingress
0s          Normal   Deleted   Namespace/web-portal   deleted ConfigMap/network-info
0s          Normal   Deleted   Namespace/web-portal   deleted NetworkPolicy.networking.k8s.io/egress
```
