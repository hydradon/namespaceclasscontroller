# 11: Someone edits or deletes a created object

The controller watches the objects it created, with one drift watch per kind. When someone deletes
one of these objects or changes it, the watch starts a run for its namespace, usually within a
second. The run applies the class again: a deleted object is created again (with a new UID), and a
changed field that the class sets gets the class's value back. Fields that the class does not set
are left alone. Right after a kind is used for the first time, its watch needs a moment to list the
existing objects; during that time the run repeats every second, so a change is still caught.

This page follows [step 5 of the demo](../demo.md#step-5-undo-changes-made-by-hand).

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

web-portal uses internal-network
([sample](../../config/samples/namespaceclass_v1alpha1_internal-network.yaml)), as after step 4 of
the demo. The drift watches for NetworkPolicy and ConfigMap are ready.

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
  name: egress
  namespace: web-portal
  uid: 1c80e697-7032-4342-b547-6f105371827a
  labels:
    namespaceclass.akuity.io/class: internal-network
  ownerReferences:
    - apiVersion: namespaceclass.akuity.io/v1alpha1
      kind: NamespaceClass
      name: internal-network
      uid: 1dc1b74b-c9bd-4ae0-a39e-533b4f3754c5
      controller: true
# spec: as in the class item (egress to 10.8.0.0/16 and to DNS)
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: ingress
  namespace: web-portal
  uid: 8fb64725-8dcd-4cd2-82b0-14336b3507a5
  resourceVersion: "690"
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

## Part 1: someone deletes an object

### Action

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete networkpolicy egress -n web-portal
networkpolicy.networking.k8s.io "egress" deleted from web-portal namespace
```

### What starts a run

The drift watch for NetworkPolicy holds the metadata of every object with the label
`namespaceclass.akuity.io/class`. It sees a delete event for `egress`. Delete events always pass the
filter, and the run is for the namespace of the object: web-portal.

### The run, step by step

1. **Read the namespace**: label internal-network; the annotation lists three objects.
2. **Previous list**: previous = `{ConfigMap/network-info, NetworkPolicy.networking.k8s.io/egress,
   NetworkPolicy.networking.k8s.io/ingress}`.
3. **Desired objects**: the same three objects.
4. **Drift watches**: ready.
5. **Live read and classify**: `egress` does not exist, so it is new. `ingress` and `network-info`
   exist and are managed. conflicts = `{}`.
6. **Record the old and the new objects**: the list does not change. Nothing is written.
7. **Apply**: `egress` is created again (`Created`). `ingress` and `network-info` do not change and
   get no event.
8. **Delete old objects**: previous − desired = `{}`.
9. **Record the current objects**: the list does not change. Nothing is written.
10. **Result**: done.

### Resulting state

`egress` exists again, with the same content, label and owner reference, and a new uid
(`b3efe566-984b-48c6-a908-ca5cdb5038b3` instead of `1c80e697-7032-4342-b547-6f105371827a`). Real
output from the demo (the age starts again from zero):

```console
$ kubectl get networkpolicy egress -n web-portal
NAME     POD-SELECTOR   AGE
egress   <none>         0s
```

### Events

```console
$ kubectl events -n default --for namespace/web-portal
LAST SEEN   TYPE     REASON    OBJECT                 MESSAGE
0s          Normal   Created   Namespace/web-portal   created NetworkPolicy.networking.k8s.io/egress
```

## Part 2: someone edits an object

### Action

Remove the ingress rules, which blocks all ingress traffic:

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl patch networkpolicy ingress -n web-portal --type=merge -p '{"spec":{"ingress":[]}}'
networkpolicy.networking.k8s.io/ingress patched
```

### What starts a run

The drift watch for NetworkPolicy sees an update event. The resourceVersion changed, so it passes
the filter (a periodic resync, with the same resourceVersion, would not). The run is for
web-portal.

### The run, step by step

Steps 1 to 4 are as in part 1. Then:

- **Live read and classify** (step 5): all three objects exist and are managed. conflicts = `{}`.
- **Record the old and the new objects** (step 6): nothing is written.
- **Apply** (step 7): Server-Side Apply with force sets `spec.ingress` back to the class's value.
  The patch had made `kubectl-patch` the field manager of `spec.ingress`; force takes the field
  back. The object changes, so the event is `Updated`. The other two objects do not change.
- Steps 8 to 10: nothing to delete, nothing to write, done.

### Resulting state

`ingress` has its ingress rule again: same uid `8fb64725-8dcd-4cd2-82b0-14336b3507a5`, a new
resourceVersion, and `cidr: 10.8.0.0/16`. Real output from the demo:

```console
$ kubectl get networkpolicy ingress -n web-portal -o jsonpath='{.spec.ingress[0].from[0].ipBlock.cidr}'
10.8.0.0/16
```

Only fields that the class sets are put back. If someone adds a field that the class does not set,
for example a label `team: web` on `ingress`, the drift watch also starts a run, but the apply
changes nothing: the label stays, and there is no event.

### Events

```console
$ kubectl events -n default --for namespace/web-portal
LAST SEEN   TYPE     REASON    OBJECT                 MESSAGE
0s          Normal   Updated   Namespace/web-portal   updated NetworkPolicy.networking.k8s.io/ingress
```

The controller's own update also reaches the drift watch and starts one more run. That run finds
nothing to change and writes nothing, so the controller does not keep updating its own objects.

## Part 3: the time right after a kind is used for the first time

A drift watch for a kind is added by the first run that uses the kind, for example the first run
after the controller starts, or the first class with a new custom resource. The watch first lists
the existing objects of its kind in the background. Until that list is done, it can miss a change.

To cover this time, the controller tracks for each kind whether its watch has finished the first
list. When a run had no error, but the watch of one of its desired kinds has not finished the list,
the run returns `RequeueAfter: 1s`. Each repeated run reads every desired object again and fixes
what changed. For example, in [demo step 2](../demo.md#step-2-use-a-class) web-portal is the first
namespace with a NetworkPolicy:

| Time | What happens |
|---|---|
| 0 s | The run adds the NetworkPolicy watch, creates `ingress` and returns `RequeueAfter: 1s`. The watch starts to list. |
| 0.3 s | Someone deletes `ingress`. If the watch has not finished its list, it may not report the deletion. |
| about 1 s | The repeated run reads `ingress`, finds it missing and creates it again. If the watch has finished its list by now (usual for a built-in kind), this run ends without asking to run again. Otherwise it asks again. |
| later | The watch reports every change, as in parts 1 and 2. |

A built-in kind usually finishes its list in well under a second. A watch that has not finished
within 30 seconds counts as ready anyway. This can happen for a kind that can be created but not
listed (such as LocalSubjectAccessReview), when the CRD of the kind was deleted right after the kind
was used, or when an aggregated API is down. The controller then logs
`Drift watch did not list its objects within the grace period` once, and the repeated runs stop.
One watch that cannot list does not hold back the watches of other kinds.
