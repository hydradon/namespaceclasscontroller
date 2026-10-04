# 09: The API server rejects one object

The API server can reject one object of a class, for example because a field cannot be changed or
an admission webhook refuses it. The controller still applies the other objects. It deletes nothing,
so the namespace never has fewer objects than before because of a broken item. It records
`ApplyFailed` with the API server's message and retries with backoff (5 milliseconds, doubling up to
5 minutes). The usual fix for a field that cannot be changed is to give the object a new name in the
class.

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

team-a uses team-baseline as in the
[sample](../../config/samples/namespaceclass_v1alpha1_team-baseline.yaml).

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: team-a
  labels:
    namespaceclass.akuity.io/name: team-baseline
  annotations:
    namespaceclass.akuity.io/managed-resources: '[{"kind":"LimitRange","name":"defaults"},{"kind":"ResourceQuota","name":"compute"},{"kind":"ServiceAccount","name":"deployer"},{"group":"rbac.authorization.k8s.io","kind":"RoleBinding","name":"deployer-view"}]'
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: deployer-view
  namespace: team-a
  resourceVersion: "1192"
  labels:
    namespaceclass.akuity.io/class: team-baseline
  ownerReferences:
    - apiVersion: namespaceclass.akuity.io/v1alpha1
      kind: NamespaceClass
      name: team-baseline
      uid: 354f2831-2586-42b4-8aa5-135bc6327803
      controller: true
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: view
subjects:
  - kind: ServiceAccount
    name: deployer
```

The ServiceAccount `deployer`, the ResourceQuota `compute` (20 pods) and the LimitRange `defaults`
also exist, with the same label and owner reference.

## Action

The admin edits team-baseline in three ways: the RoleBinding `deployer-view` now refers to the
ClusterRole `edit` instead of `view`, the quota allows 30 pods instead of 20, and the LimitRange
item is removed.

```yaml
apiVersion: namespaceclass.akuity.io/v1alpha1
kind: NamespaceClass
metadata:
  name: team-baseline
spec:
  resources:
    - apiVersion: v1
      kind: ServiceAccount
      metadata:
        name: deployer
    - apiVersion: v1
      kind: ResourceQuota
      metadata:
        name: compute
      spec:
        hard:
          requests.cpu: "4"
          requests.memory: 8Gi
          limits.cpu: "8"
          limits.memory: 16Gi
          pods: "30" # was "20"
    # The LimitRange item is removed.
    - apiVersion: rbac.authorization.k8s.io/v1
      kind: RoleBinding
      metadata:
        name: deployer-view
      roleRef:
        apiGroup: rbac.authorization.k8s.io
        kind: ClusterRole
        name: edit # was view; a RoleBinding cannot change its roleRef
      subjects:
        - kind: ServiceAccount
          name: deployer
```

Save the class above as `team-baseline.yaml` and apply it:

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl apply -f team-baseline.yaml
namespaceclass.namespaceclass.akuity.io/team-baseline configured
```

## What starts a run

The NamespaceClass watch sees an update event with a new `metadata.generation`, which passes the
filter. It maps to team-a. After that, each failed run is retried by the backoff.

## The run, step by step

1. **Read the namespace**: team-a has the label `namespaceclass.akuity.io/name: team-baseline`. The
   annotation lists four objects.
2. **Previous list**: previous = `{LimitRange/defaults, ResourceQuota/compute,
   ServiceAccount/deployer, RoleBinding.rbac.authorization.k8s.io/deployer-view}`.
3. **Desired objects**: `ServiceAccount/deployer`, `ResourceQuota/compute`,
   `RoleBinding.rbac.authorization.k8s.io/deployer-view`. The class passes every check.
4. **Drift watches**: all three kinds have ready watches.
5. **Live read and classify**: all three exist and are managed. conflicts = `{}`.
6. **Record the old and the new objects**: previous ∪ (desired − conflicts) = previous. Nothing is
   written.
7. **Apply**, in the order of the class:
   - `ServiceAccount/deployer`: no change, no event.
   - `ResourceQuota/compute`: now 30 pods. Event `Updated`.
   - `RoleBinding.rbac.authorization.k8s.io/deployer-view`: the API server rejects the change of
     `roleRef`. Event `ApplyFailed` with the API server's message. The controller goes on with the
     next item (there is none here).
8. **Delete old objects**: skipped, because an item failed. `LimitRange/defaults` stays, although
   the class no longer has it.
9. **Record the current objects**: skipped too. The annotation keeps the list of step 6, so a later
   run can still delete the LimitRange.
10. **Result**: the run returns the error, so it is retried with backoff. Each retry applies the
    ResourceQuota again (no change now, no event) and fails on the RoleBinding again.

## Resulting state

- ResourceQuota `compute`: allows 30 pods.
- RoleBinding `deployer-view`: unchanged. It still refers to `view` and keeps its resourceVersion
  `"1192"`.
- LimitRange `defaults`: still there.
- The annotation is unchanged. It still lists the LimitRange.

## Events

The message comes from the API server. It is from Kubernetes 1.37 (the envtest version); other
versions can word it differently.

```console
$ kubectl events -n default --for namespace/team-a
LAST SEEN           TYPE      REASON        OBJECT             MESSAGE
40s                 Normal    Updated       Namespace/team-a   updated ResourceQuota/compute
40s (x2 over 40s)   Warning   ApplyFailed   Namespace/team-a   cannot apply RoleBinding.rbac.authorization.k8s.io/deployer-view: RoleBinding.rbac.authorization.k8s.io "deployer-view" is invalid: roleRef: Invalid value: {"APIGroup":"rbac.authorization.k8s.io","Kind":"ClusterRole","Name":"edit"}: field is immutable
0s                  Normal    Created       Namespace/team-a   created RoleBinding.rbac.authorization.k8s.io/deployer-edit
0s                  Normal    Deleted       Namespace/team-a   deleted LimitRange/defaults
0s                  Normal    Deleted       Namespace/team-a   deleted RoleBinding.rbac.authorization.k8s.io/deployer-view
```

The first two lines come from the edit above; the retries merge into the `ApplyFailed` event. The
last three lines come from the fix below.

## Fixing it: rename the object

Give the RoleBinding a new name in the class, `deployer-edit`, and apply the class again. The other
items stay as in the edit above.

The new generation starts a run:

1. **Read the namespace**: as before.
2. **Previous list**: the same four objects as before, including `LimitRange/defaults`.
3. **Desired objects**: `ServiceAccount/deployer`, `ResourceQuota/compute`,
   `RoleBinding.rbac.authorization.k8s.io/deployer-edit`.
4. **Drift watches**: all three kinds have ready watches.
5. **Live read and classify**: `deployer-edit` does not exist, so it is new. The other two exist and
   are managed. conflicts = `{}`.
6. **Record the old and the new objects**: the controller adds `deployer-edit` and writes
   `[{"kind":"LimitRange","name":"defaults"},{"kind":"ResourceQuota","name":"compute"},{"kind":"ServiceAccount","name":"deployer"},{"group":"rbac.authorization.k8s.io","kind":"RoleBinding","name":"deployer-edit"},{"group":"rbac.authorization.k8s.io","kind":"RoleBinding","name":"deployer-view"}]`
7. **Apply**: `deployer-edit` is created (`Created`). The other two do not change.
8. **Delete old objects**: every item was applied, so the controller deletes previous − desired =
   `{LimitRange/defaults, RoleBinding.rbac.authorization.k8s.io/deployer-view}` (`Deleted` for each).
9. **Record the current objects**: the controller writes
   `[{"kind":"ResourceQuota","name":"compute"},{"kind":"ServiceAccount","name":"deployer"},{"group":"rbac.authorization.k8s.io","kind":"RoleBinding","name":"deployer-edit"}]`
10. **Result**: done. The retries stop.

Resulting state: the RoleBinding `deployer-edit` refers to `edit`; `deployer-view` and the LimitRange
`defaults` are gone; the ResourceQuota allows 30 pods.
