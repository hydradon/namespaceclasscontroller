# 15: The managed-resources annotation cannot be read

The annotation `namespaceclass.akuity.io/managed-resources` is written by the controller and should
not be edited. If someone breaks it, so that it is no longer a JSON list of `{group, kind, name}`,
the next run records an `InvalidAnnotation` warning and treats the previous list as empty. Nothing
is deleted based on a list that cannot be read. When the run gets past the class checks, it writes
the annotation again with the desired objects; a run that stops with `ClassNotFound`,
`InvalidClass` or a kind that is not installed writes nothing (see the end of
[Resulting state](#resulting-state)). Objects that only the broken list named, and that the class no
longer wants, stay in the namespace.

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

billing uses internal-network after `docs/demo/internal-network-v2.yaml` was applied
([demo step 4](../demo.md#step-4-update-a-class)), so it has four objects, including the
NetworkPolicy `allow-monitoring`.

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: billing
  labels:
    namespaceclass.akuity.io/name: internal-network
  annotations:
    namespaceclass.akuity.io/managed-resources: '[{"kind":"ConfigMap","name":"network-info"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"allow-monitoring"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"egress"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]'
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-monitoring
  namespace: billing
  uid: 1883d1d4-a5b3-48b5-ba1c-1e3f172999a4
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
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: monitoring
```

`ingress`, `egress` and `network-info` use the VPN range `10.9.0.0/16` of version 2.

## Action

First, someone edits the annotation by hand with
`KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl edit namespace billing` and leaves
a comma after the last entry:

```yaml
    namespaceclass.akuity.io/managed-resources: '[{"kind":"ConfigMap","name":"network-info"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"allow-monitoring"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"egress"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"},]'
```

Later, the admin applies the original internal-network again. It has no `allow-monitoring` and uses
the range `10.8.0.0/16`:

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl apply -f config/samples/namespaceclass_v1alpha1_internal-network.yaml
namespaceclass.namespaceclass.akuity.io/internal-network configured
```

## What starts a run

- The hand edit: the Namespace watch sees an update event, but the label did not change, so the
  event does not pass the filter. No run starts, and the broken annotation stays as it is for now.
- The class edit: the NamespaceClass watch sees an update with a new `metadata.generation`. It maps
  to every namespace whose label is internal-network, billing among them.

## The run, step by step

1. **Read the namespace**: billing has the label `namespaceclass.akuity.io/name: internal-network`.
2. **Previous list**: the annotation cannot be read. Warning `InvalidAnnotation`. previous = `{}`.
3. **Desired objects**: the original internal-network: `NetworkPolicy.networking.k8s.io/ingress`,
   `NetworkPolicy.networking.k8s.io/egress`, `ConfigMap/network-info`.
4. **Drift watches**: ready.
5. **Live read and classify**: all three exist and are managed. conflicts = `{}`.
6. **Record the old and the new objects**: previous ∪ (desired − conflicts) = the three desired
   objects. This text differs from the broken annotation, so the controller writes
   `[{"kind":"ConfigMap","name":"network-info"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"egress"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]`
7. **Apply**: the three objects get the range `10.8.0.0/16` back. Event `Updated` for each.
8. **Delete old objects**: previous − desired = `{}`. The controller cannot know that
   `allow-monitoring` was listed, so it does not delete it.
9. **Record the current objects**: the same list as in step 6. Nothing is written.
10. **Result**: done.

The next run of billing reads a valid annotation again, so the warning does not come back.

## Resulting state

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: billing
  labels:
    namespaceclass.akuity.io/name: internal-network
  annotations:
    namespaceclass.akuity.io/managed-resources: '[{"kind":"ConfigMap","name":"network-info"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"egress"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]'
```

What changed:

- The annotation is valid again and lists the three objects of the class.
- `ingress`, `egress` and `network-info` use `10.8.0.0/16` again.
- `allow-monitoring` is still in billing, with its label and owner reference. It is not in the
  annotation, so the controller will not delete it later either. Delete it by hand:
  `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete networkpolicy allow-monitoring -n billing`.
  The drift watch sees the deletion and starts a run, which changes nothing because the class does
  not want the object.

For comparison, web-portal also uses internal-network but has a valid annotation. Its run deletes
`allow-monitoring` as usual ([use case 03](03-edit-class.md)).

Two more cases:

- If the run stops at step 3 (`ClassNotFound`, `InvalidClass` or a kind that is not installed), it
  has written nothing, so the broken annotation stays. Each later run records the warning again,
  until a run gets to step 6.
- If the label is removed while the annotation cannot be read, previous and desired are both empty.
  The run removes the annotation (an empty list means no annotation) and deletes nothing. The
  objects stay in the namespace.

## Events

```console
$ kubectl events -n default --for namespace/billing
LAST SEEN   TYPE      REASON              OBJECT              MESSAGE
0s          Warning   InvalidAnnotation   Namespace/billing   annotation namespaceclass.akuity.io/managed-resources cannot be read (expected a JSON list of {group, kind, name}: invalid character ']' looking for beginning of value); it is treated as empty, so objects it listed that are no longer wanted are left in place
0s          Normal    Updated             Namespace/billing   updated NetworkPolicy.networking.k8s.io/ingress
0s          Normal    Updated             Namespace/billing   updated NetworkPolicy.networking.k8s.io/egress
0s          Normal    Updated             Namespace/billing   updated ConfigMap/network-info
```

Other ways to break the list give other reasons in the parentheses, for example
`entry 1 has no name` for an entry without a name.
