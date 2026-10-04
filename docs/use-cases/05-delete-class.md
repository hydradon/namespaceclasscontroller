# 05: Deleting a class

Every object the controller creates has an owner reference to its class. When the class is deleted,
the Kubernetes garbage collector deletes these objects; the controller deletes nothing itself. A
deletion with `--cascade=orphan` keeps the objects. Namespaces that still have the label get a
`ClassNotFound` warning and nothing else changes. When the class is created again, the controller
creates the objects again, or, if they were kept, points their owner reference to the new class.

This page has four parts: delete the class, create it again, delete it with `--cascade=orphan`,
create it again. [Step 10 of the demo](../demo.md#step-10-delete-a-class) shows the first part with
internal-network.

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

team-baseline ([sample](../../config/samples/namespaceclass_v1alpha1_team-baseline.yaml)) is used
by team-a ([sample](../../config/samples/namespace_team-a.yaml)).

```yaml
apiVersion: namespaceclass.akuity.io/v1alpha1
kind: NamespaceClass
metadata:
  name: team-baseline
  uid: 354f2831-2586-42b4-8aa5-135bc6327803
# spec: as in the sample (ServiceAccount deployer, ResourceQuota compute, LimitRange defaults,
# RoleBinding deployer-view)
---
apiVersion: v1
kind: Namespace
metadata:
  name: team-a
  labels:
    namespaceclass.akuity.io/name: team-baseline
  annotations:
    namespaceclass.akuity.io/managed-resources: '[{"kind":"LimitRange","name":"defaults"},{"kind":"ResourceQuota","name":"compute"},{"kind":"ServiceAccount","name":"deployer"},{"group":"rbac.authorization.k8s.io","kind":"RoleBinding","name":"deployer-view"}]'
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: deployer
  namespace: team-a
  uid: 7b1781e3-574d-47c4-a305-65b88a34f1c7
  labels:
    namespaceclass.akuity.io/class: team-baseline
  ownerReferences:
    - apiVersion: namespaceclass.akuity.io/v1alpha1
      kind: NamespaceClass
      name: team-baseline
      uid: 354f2831-2586-42b4-8aa5-135bc6327803
      controller: true
```

The ResourceQuota `compute`, the LimitRange `defaults` and the RoleBinding `deployer-view` in team-a
have the same label and owner reference as the ServiceAccount.

## Part 1: delete the class

### Action

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete namespaceclass team-baseline
namespaceclass.namespaceclass.akuity.io "team-baseline" deleted
```

The class is deleted right away. Then the garbage collector deletes the four objects in team-a,
because their owner is gone. The first class deletion after the CRD is installed can take up to
about 30 seconds, because the garbage collector must first discover the new CRD.

### What starts a run

- The NamespaceClass watch sees a delete event. Delete events always pass the filter. The controller
  lists the namespaces whose label `namespaceclass.akuity.io/name` is team-baseline: team-a.
- Each deletion by the garbage collector reaches a drift watch and starts another run for team-a.

### The run, step by step

Every run for team-a goes like this:

1. **Read the namespace**: team-a still has the label `namespaceclass.akuity.io/name: team-baseline`
   and the annotation with four objects.
2. **Previous list**: previous = `{LimitRange/defaults, ResourceQuota/compute,
   ServiceAccount/deployer, RoleBinding.rbac.authorization.k8s.io/deployer-view}`.
3. **Desired objects**: the class does not exist. Warning `ClassNotFound`. The run stops here and
   writes nothing: no annotation write, no apply, no delete. It returns no error, so it is not
   retried.

### Resulting state

- The four objects in team-a are gone. The garbage collector deleted them, not the controller.
- team-a still has the label and the annotation. The annotation still lists the four objects.

If team-a opts out now, the run finds the four objects already gone. It records no `Deleted` event
and removes the annotation.

### Events

```console
$ kubectl events -n default --for namespace/team-a
LAST SEEN         TYPE      REASON          OBJECT             MESSAGE
1s (x2 over 1s)   Warning   ClassNotFound   Namespace/team-a   NamespaceClass "team-baseline" does not exist; nothing was changed
```

The class event and the garbage collector's deletions start more than one run. Their warnings are
the same, so they are merged into one event with a count. The demo shows the same for legacy.

## Part 2: create the class again

### Action

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl apply -f config/samples/namespaceclass_v1alpha1_team-baseline.yaml
namespaceclass.namespaceclass.akuity.io/team-baseline created
```

The new class has a new UID: `c8190b9b-5a58-47da-8cac-2136555abef1`.

### What starts a run

The NamespaceClass watch sees a create event. Create events always pass the filter. The controller
lists the namespaces whose label is team-baseline: team-a.

### The run, step by step

1. **Read the namespace**: label team-baseline; the annotation still lists four objects.
2. **Previous list**: the same four objects as in part 1.
3. **Desired objects**: the same four objects, now with an owner reference to the new UID.
4. **Drift watches**: the four kinds already have ready watches.
5. **Live read and classify**: none of the four objects exists, so all are new. conflicts = `{}`.
6. **Record the old and the new objects**: the list does not change, so nothing is written.
7. **Apply**: the four objects are created. Event `Created` for each.
8. **Delete old objects**: previous − desired = `{}`.
9. **Record the current objects**: the list does not change, so nothing is written.
10. **Result**: done.

### Resulting state

The four objects exist again, with new UIDs (for example the ServiceAccount `deployer` now has the
uid `e6cebd2c-cbea-4ddf-af09-6698f6a23759`) and an owner reference to the new class
(`uid: c8190b9b-5a58-47da-8cac-2136555abef1`). The annotation is unchanged.

### Events

```console
$ kubectl events -n default --for namespace/team-a
LAST SEEN   TYPE     REASON    OBJECT             MESSAGE
0s          Normal   Created   Namespace/team-a   created ServiceAccount/deployer
0s          Normal   Created   Namespace/team-a   created ResourceQuota/compute
0s          Normal   Created   Namespace/team-a   created LimitRange/defaults
0s          Normal   Created   Namespace/team-a   created RoleBinding.rbac.authorization.k8s.io/deployer-view
```

## Part 3: delete the class with `--cascade=orphan`

### Action

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete namespaceclass team-baseline --cascade=orphan
namespaceclass.namespaceclass.akuity.io "team-baseline" deleted
```

The API server does not delete the class right away. It sets a deletion timestamp and adds the
finalizer `orphan`. The garbage collector removes the owner reference from each of the four
objects, then removes the finalizer, and the class is gone. The objects stay.

### What starts a run

- The NamespaceClass watch sees an update event: the deletion timestamp was set. This also increases
  `metadata.generation`, so the event passes the filter. It maps to team-a.
- Each owner reference removal is an update with a new resourceVersion. The drift watch sees it
  (the objects still have the class label) and starts a run for team-a.
- When the class is gone, the delete event maps to team-a.

### The run, step by step

Steps 1 and 2 are as in part 1. Step 3 stops every run with `ClassNotFound`:

- While the class still exists with its deletion timestamp: `NamespaceClass "team-baseline" is
  being deleted; nothing was changed`.
- After the class is gone: `NamespaceClass "team-baseline" does not exist; nothing was changed`.

Nothing is written. Because the controller does not apply a class that is being deleted, it does not
put back the owner references that the garbage collector removes.

### Resulting state

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: deployer
  namespace: team-a
  uid: e6cebd2c-cbea-4ddf-af09-6698f6a23759
  labels:
    namespaceclass.akuity.io/class: team-baseline
```

What changed:

- The four objects still exist, with the same UIDs.
- They have no owner reference any more. They still have the label
  `namespaceclass.akuity.io/class: team-baseline`, so the controller still manages them.
- team-a still has the label and the annotation. If team-a opts out now, the controller deletes the
  four objects.

### Events

```console
$ kubectl events -n default --for namespace/team-a
LAST SEEN         TYPE      REASON          OBJECT             MESSAGE
2s (x2 over 2s)   Warning   ClassNotFound   Namespace/team-a   NamespaceClass "team-baseline" is being deleted; nothing was changed
1s                Warning   ClassNotFound   Namespace/team-a   NamespaceClass "team-baseline" does not exist; nothing was changed
```

The number of runs, and so the counts, depend on timing. team-a has not changed since part 1, so
when this part runs within a few minutes of part 1, the second warning can be merged into the event
of part 1 instead of appearing as a new line.

## Part 4: create the class again

### Action

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl apply -f config/samples/namespaceclass_v1alpha1_team-baseline.yaml
namespaceclass.namespaceclass.akuity.io/team-baseline created
```

The new class has the UID `a0a2b112-6851-4879-a096-64fa981cdc67`.

### What starts a run

The create event of the class, as in part 2.

### The run, step by step

Steps 1 to 4 are as in part 2. Then:

- **Live read and classify** (step 5): the four objects exist and have the class label, so they are
  managed. conflicts = `{}`.
- **Record the old and the new objects** (step 6): the list does not change, so nothing is written.
- **Apply** (step 7): each object gets the owner reference to the new class. Each one changes, so
  each gets the event `Updated`.
- Steps 8 to 10: nothing to delete, nothing to write, done.

### Resulting state

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: deployer
  namespace: team-a
  uid: e6cebd2c-cbea-4ddf-af09-6698f6a23759
  labels:
    namespaceclass.akuity.io/class: team-baseline
  ownerReferences:
    - apiVersion: namespaceclass.akuity.io/v1alpha1
      kind: NamespaceClass
      name: team-baseline
      uid: a0a2b112-6851-4879-a096-64fa981cdc67
      controller: true
```

What changed: the owner reference is back and points to the new class UID. The object UIDs did not
change.

### Events

```console
$ kubectl events -n default --for namespace/team-a
LAST SEEN   TYPE     REASON    OBJECT             MESSAGE
0s          Normal   Updated   Namespace/team-a   updated ServiceAccount/deployer
0s          Normal   Updated   Namespace/team-a   updated ResourceQuota/compute
0s          Normal   Updated   Namespace/team-a   updated LimitRange/defaults
0s          Normal   Updated   Namespace/team-a   updated RoleBinding.rbac.authorization.k8s.io/deployer-view
```
