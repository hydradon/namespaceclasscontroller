# 12: Someone removes the controller's label from an object

The controller treats an object as its own ("managed") when it has the label
`namespaceclass.akuity.io/class` or an owner reference to a NamespaceClass. Either one is enough.
So when someone removes only the label from a created object, the owner reference still marks it as
managed, and the next run puts the label back. When someone removes both the label and the owner
reference, the object is no longer managed: the controller treats it like any existing object
([use case 10](10-existing-object.md)). It leaves the object unchanged, records `Conflict`, removes
it from the annotation and never deletes it.

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

billing uses internal-network ([sample](../../config/samples/namespace_billing.yaml)).

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: billing
  labels:
    namespaceclass.akuity.io/name: internal-network
  annotations:
    namespaceclass.akuity.io/managed-resources: '[{"kind":"ConfigMap","name":"network-info"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"egress"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]'
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: network-info
  namespace: billing
  uid: f4f7fd60-e070-4fee-b3d4-17cb6af1a36a
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

## Part 1: only the label is removed

### Action

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl label configmap network-info -n billing namespaceclass.akuity.io/class-
configmap/network-info unlabeled
```

### What starts a run

The drift watches hold only objects with the label `namespaceclass.akuity.io/class`. Without the
label, the ConfigMap leaves the cache of the ConfigMap watch, which reports this as a delete event.
Delete events always pass the filter, so a run starts for billing.

### The run, step by step

1. **Read the namespace**: label internal-network; the annotation lists three objects.
2. **Previous list**: previous = `{ConfigMap/network-info, NetworkPolicy.networking.k8s.io/egress,
   NetworkPolicy.networking.k8s.io/ingress}`.
3. **Desired objects**: the same three objects.
4. **Drift watches**: ready.
5. **Live read and classify**: `network-info` has no class label, but it has an owner reference to
   the NamespaceClass internal-network, so it is managed. The two NetworkPolicies are managed too.
   conflicts = `{}`.
6. **Record the old and the new objects**: the list does not change. Nothing is written.
7. **Apply**: the controller applies the label with the rest of the object, so the label is set
   again. The object changes, so the event is `Updated`. The NetworkPolicies do not change.
8. **Delete old objects**: previous − desired = `{}`.
9. **Record the current objects**: nothing is written.
10. **Result**: done.

With the label back, the ConfigMap is in the cache of the watch again. The watch reports it as a
create event, which does not pass the filter, so no further run starts.

### Resulting state

The label `namespaceclass.akuity.io/class: internal-network` is back. Same uid, same data, new
resourceVersion. The annotation is unchanged.

### Events

```console
$ kubectl events -n default --for namespace/billing
LAST SEEN   TYPE     REASON    OBJECT              MESSAGE
0s          Normal   Updated   Namespace/billing   updated ConfigMap/network-info
```

## Part 2: the label and the owner reference are removed

### Action

Remove both in one command. If they are removed one after the other, the run that starts after the
first command can put the label back before the second command runs.

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl patch configmap network-info -n billing --type=merge -p '{"metadata":{"labels":{"namespaceclass.akuity.io/class":null},"ownerReferences":null}}'
configmap/network-info patched
```

### What starts a run

As in part 1: the ConfigMap loses the label and leaves the cache, which the watch reports as a
delete event.

### The run, step by step

1. **Read the namespace**: as in part 1.
2. **Previous list**: the same three objects.
3. **Desired objects**: the same three objects.
4. **Drift watches**: ready.
5. **Live read and classify**: `network-info` has no class label and no owner reference to a
   NamespaceClass, so it is not managed. Warning `Conflict`. conflicts = `{ConfigMap/network-info}`.
6. **Record the old and the new objects**: previous ∪ (desired − conflicts) = previous, because
   `network-info` is in previous. Nothing is written.
7. **Apply**: the two NetworkPolicies do not change. `network-info` is not applied.
8. **Delete old objects**: previous − desired = `{}`. `network-info` is still desired, so it is not a
   candidate for deletion.
9. **Record the current objects**: (desired − conflicts) ∪ failed deletions =
   `{NetworkPolicy.networking.k8s.io/egress, NetworkPolicy.networking.k8s.io/ingress}`. The controller
   writes
   `[{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"egress"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]`
10. **Result**: done. The conflict is not retried.

### Resulting state

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: network-info
  namespace: billing
  uid: f4f7fd60-e070-4fee-b3d4-17cb6af1a36a
data:
  vpnCIDR: 10.8.0.0/16
```

What changed:

- `network-info` has no class label and no owner reference. The controller does not change it.
  Someone can now edit it by hand.
- The annotation no longer lists it, so the controller never deletes it, also not when billing opts
  out or when the class drops the item.
- Later changes to `network-info` start no run, because the drift watch no longer sees it. Any run
  for billing that happens for another reason records the `Conflict` warning again, as in
  [use case 10](10-existing-object.md).

### Events

```console
$ kubectl events -n default --for namespace/billing
LAST SEEN   TYPE      REASON     OBJECT              MESSAGE
0s          Warning   Conflict   Namespace/billing   ConfigMap/network-info already exists and was not created by namespaceclass-controller; it is left unchanged
```
