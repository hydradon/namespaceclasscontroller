# 03: Editing a class

When a class changes, every namespace that uses it gets a run. In each run the controller creates
the added items, updates the changed items and deletes the removed items. A field removed from an
item is also removed from the live object, because Server-Side Apply removes the fields that the
controller set before and no longer sets. Items that did not change are applied too, but the apply
changes nothing, so they get no event.

This page edits team-baseline. [Step 4 of the demo](../demo.md#step-4-update-a-class) shows a class
edit with real output: `internal-network-v2.yaml` changes the VPN range of three items and adds
`allow-monitoring` in both web-portal and billing.

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

team-baseline as in the [sample](../../config/samples/namespaceclass_v1alpha1_team-baseline.yaml).
It is used by team-a ([sample](../../config/samples/namespace_team-a.yaml)) and by team-b, a second
namespace with the same label for this example.

```yaml
apiVersion: namespaceclass.akuity.io/v1alpha1
kind: NamespaceClass
metadata:
  name: team-baseline
  uid: 354f2831-2586-42b4-8aa5-135bc6327803
  generation: 1
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
          pods: "20"
    - apiVersion: v1
      kind: LimitRange
      metadata:
        name: defaults
      spec:
        limits:
          - type: Container
            defaultRequest:
              cpu: 100m
              memory: 128Mi
            default:
              cpu: 500m
              memory: 256Mi
    - apiVersion: rbac.authorization.k8s.io/v1
      kind: RoleBinding
      metadata:
        name: deployer-view
      roleRef:
        apiGroup: rbac.authorization.k8s.io
        kind: ClusterRole
        name: view
      subjects:
        - kind: ServiceAccount
          name: deployer
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
kind: ResourceQuota
metadata:
  name: compute
  namespace: team-a
  uid: 2b34b140-f655-4019-9a75-0d6c9cf0725b
  resourceVersion: "1180"
  labels:
    namespaceclass.akuity.io/class: team-baseline
  ownerReferences:
    - apiVersion: namespaceclass.akuity.io/v1alpha1
      kind: NamespaceClass
      name: team-baseline
      uid: 354f2831-2586-42b4-8aa5-135bc6327803
      controller: true
spec:
  hard:
    limits.cpu: "8"
    limits.memory: 16Gi
    pods: "20"
    requests.cpu: "4"
    requests.memory: 8Gi
```

The other three objects in team-a (ServiceAccount `deployer`, LimitRange `defaults`, RoleBinding
`deployer-view`) have the same label and owner reference. team-b looks the same, with its own
objects.

## Action

Apply the edited class. It changes three things: the quota no longer limits the number of pods,
the default memory limit goes up, and the deployer gets the `edit` role instead of `view`. A
RoleBinding cannot change its `roleRef`, so the new role comes with a new RoleBinding name (see
[use case 09](09-rejected-item.md) for what happens otherwise).

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
          # pods: "20" is removed
    - apiVersion: v1
      kind: LimitRange
      metadata:
        name: defaults
      spec:
        limits:
          - type: Container
            defaultRequest:
              cpu: 100m
              memory: 128Mi
            default:
              cpu: 500m
              memory: 512Mi # was 256Mi
    - apiVersion: rbac.authorization.k8s.io/v1
      kind: RoleBinding
      metadata:
        name: deployer-edit # new item; deployer-view is removed
      roleRef:
        apiGroup: rbac.authorization.k8s.io
        kind: ClusterRole
        name: edit
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

The NamespaceClass watch sees an update event. The spec changed, so `metadata.generation` went from
1 to 2, and the event passes the filter. The controller lists the namespaces whose label
`namespaceclass.akuity.io/name` is team-baseline: team-a and team-b. Each one gets its own run.

## The run, step by step

The run for team-a. The run for team-b is the same, with its own objects.

1. **Read the namespace**: team-a has the label `namespaceclass.akuity.io/name: team-baseline`. The
   annotation lists four objects.
2. **Previous list**: previous = `{LimitRange/defaults, ResourceQuota/compute,
   ServiceAccount/deployer, RoleBinding.rbac.authorization.k8s.io/deployer-view}`.
3. **Desired objects**, in the order of the class: `ServiceAccount/deployer`,
   `ResourceQuota/compute`, `LimitRange/defaults`, `RoleBinding.rbac.authorization.k8s.io/deployer-edit`.
4. **Drift watches**: all four kinds already have a ready drift watch.
5. **Live read and classify**: the first three exist and are managed. `deployer-edit` does not exist,
   so it is new. conflicts = `{}`.
6. **Record the old and the new objects**: previous ∪ (desired − conflicts) adds `deployer-edit`. The
   controller writes the list before it creates the RoleBinding:
   `[{"kind":"LimitRange","name":"defaults"},{"kind":"ResourceQuota","name":"compute"},{"kind":"ServiceAccount","name":"deployer"},{"group":"rbac.authorization.k8s.io","kind":"RoleBinding","name":"deployer-edit"},{"group":"rbac.authorization.k8s.io","kind":"RoleBinding","name":"deployer-view"}]`
7. **Apply**, in the order of the class:
   - `ServiceAccount/deployer`: the apply changes nothing. The resourceVersion stays the same, so
     there is no event.
   - `ResourceQuota/compute`: the controller applied `spec.hard.pods` before and does not apply it
     now, so Server-Side Apply removes it. Event `Updated`.
   - `LimitRange/defaults`: the default memory changes to `512Mi`. Event `Updated`.
   - `RoleBinding.rbac.authorization.k8s.io/deployer-edit`: created. Event `Created`.
8. **Delete old objects**: every item was applied, so the controller deletes previous − desired =
   `{RoleBinding.rbac.authorization.k8s.io/deployer-view}`. Event `Deleted`.
9. **Record the current objects**: the controller writes
   `[{"kind":"LimitRange","name":"defaults"},{"kind":"ResourceQuota","name":"compute"},{"kind":"ServiceAccount","name":"deployer"},{"group":"rbac.authorization.k8s.io","kind":"RoleBinding","name":"deployer-edit"}]`
10. **Result**: done.

## Resulting state

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: team-a
  labels:
    namespaceclass.akuity.io/name: team-baseline
  annotations:
    namespaceclass.akuity.io/managed-resources: '[{"kind":"LimitRange","name":"defaults"},{"kind":"ResourceQuota","name":"compute"},{"kind":"ServiceAccount","name":"deployer"},{"group":"rbac.authorization.k8s.io","kind":"RoleBinding","name":"deployer-edit"}]'
---
apiVersion: v1
kind: ResourceQuota
metadata:
  name: compute
  namespace: team-a
  uid: 2b34b140-f655-4019-9a75-0d6c9cf0725b
  resourceVersion: "1342"
  labels:
    namespaceclass.akuity.io/class: team-baseline
  ownerReferences:
    - apiVersion: namespaceclass.akuity.io/v1alpha1
      kind: NamespaceClass
      name: team-baseline
      uid: 354f2831-2586-42b4-8aa5-135bc6327803
      controller: true
spec:
  hard:
    limits.cpu: "8"
    limits.memory: 16Gi
    requests.cpu: "4"
    requests.memory: 8Gi
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: deployer-edit
  namespace: team-a
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
  name: edit
subjects:
  - kind: ServiceAccount
    name: deployer
```

What changed in team-a (and in the same way in team-b):

- ServiceAccount `deployer`: unchanged, same resourceVersion, no event.
- ResourceQuota `compute`: `spec.hard.pods` removed. Same uid, new resourceVersion.
- LimitRange `defaults`: the default memory changed from `256Mi` to `512Mi`.
- RoleBinding `deployer-edit`: new.
- RoleBinding `deployer-view`: deleted, after every item was applied.
- The annotation lists `deployer-edit` instead of `deployer-view`.

## Events

```console
$ kubectl events -n default --for namespace/team-a
LAST SEEN   TYPE     REASON    OBJECT             MESSAGE
0s          Normal   Updated   Namespace/team-a   updated ResourceQuota/compute
0s          Normal   Updated   Namespace/team-a   updated LimitRange/defaults
0s          Normal   Created   Namespace/team-a   created RoleBinding.rbac.authorization.k8s.io/deployer-edit
0s          Normal   Deleted   Namespace/team-a   deleted RoleBinding.rbac.authorization.k8s.io/deployer-view
```

`kubectl events -n default --for namespace/team-b` shows the same four events for team-b.
