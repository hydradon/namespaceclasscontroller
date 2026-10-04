# 01: A namespace starts using a class

A namespace uses a class when it has the label `namespaceclass.akuity.io/name` with the name of the
class. The label can be set when the namespace is created, or added later; both start the same run.
The controller creates every object of the class in the namespace, marks each one as its own, and
lists them in the annotation `namespaceclass.akuity.io/managed-resources` of the namespace.

This page follows [step 2 of the demo](../demo.md#step-2-use-a-class).

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

The class public-network exists
([sample](../../config/samples/namespaceclass_v1alpha1_public-network.yaml)). The namespace
`web-portal` does not exist yet.

```yaml
apiVersion: namespaceclass.akuity.io/v1alpha1
kind: NamespaceClass
metadata:
  name: public-network
  uid: 132c4c91-733d-4819-8cd5-dada4a0b5bf5
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
```

## Action

Create the namespace with the label ([sample](../../config/samples/namespace_web-portal.yaml)):

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl apply -f config/samples/namespace_web-portal.yaml
namespace/web-portal created
```

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: web-portal
  labels:
    namespaceclass.akuity.io/name: public-network
```

Or add the label to a namespace that already exists:

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl label namespace web-portal namespaceclass.akuity.io/name=public-network
namespace/web-portal labeled
```

## What starts a run

- Created with the label: the Namespace watch sees a create event. It passes the filter because the
  namespace has the label `namespaceclass.akuity.io/name`.
- Label added later: the Namespace watch sees an update event. It passes the filter because the
  label was added: the old namespace has no label, the new one has it.

Either way, one run starts for web-portal.

## The run, step by step

1. **Read the namespace** from the API server: web-portal has the label
   `namespaceclass.akuity.io/name: public-network` and no annotation.
2. **Previous list**: there is no annotation, so previous = `{}`.
3. **Desired objects**: public-network exists, is not being deleted and passes every check.
   desired = `{NetworkPolicy.networking.k8s.io/ingress}`. The object gets the namespace web-portal,
   the label `namespaceclass.akuity.io/class: public-network` and an owner reference to
   public-network.
4. **Drift watches**: in the demo, this is the first run that uses NetworkPolicy since the
   controller started. So the run adds the drift watch for NetworkPolicy. The watch has not listed
   its objects yet.
5. **Live read and classify**: `NetworkPolicy.networking.k8s.io/ingress` does not exist, so it is
   new. conflicts = `{}`.
6. **Record the old and the new objects**: previous ∪ (desired − conflicts) =
   `{NetworkPolicy.networking.k8s.io/ingress}`. The namespace has no annotation yet, so the
   controller writes it before it creates anything:
   `[{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]`
7. **Apply**: Server-Side Apply creates the NetworkPolicy. Event `Created`.
8. **Delete old objects**: previous − desired = `{}`. Nothing to delete.
9. **Record the current objects**: (desired − conflicts) ∪ failed deletions =
   `{NetworkPolicy.networking.k8s.io/ingress}`. The annotation already holds this text, so nothing
   is written.
10. **Result**: no error. The NetworkPolicy watch was added in step 4 and has not finished its first
    list, so the run returns `RequeueAfter: 1s`. The next run, about one second later, finds the
    object as the class wants it and writes nothing. Once the watch has listed its objects, a run
    ends without asking to run again (see [use case 11](11-drift.md)).

The controller's own writes do not start more runs here: the annotation write does not change the
label, and the drift watch ignores create events.

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

- New NetworkPolicy `ingress` in web-portal, with the spec of the class item.
- It has the label `namespaceclass.akuity.io/class: public-network`. The label marks it as created
  by the controller and names its class.
- It has an owner reference to public-network (`controller: true`). The garbage collector deletes it
  when the class is deleted ([use case 05](05-delete-class.md)).
- web-portal has the new annotation, which lists the NetworkPolicy.

## Events

Real output from the demo:

```console
$ kubectl events -n default --for namespace/web-portal
LAST SEEN   TYPE     REASON    OBJECT                 MESSAGE
0s          Normal   Created   Namespace/web-portal   created NetworkPolicy.networking.k8s.io/ingress
```
