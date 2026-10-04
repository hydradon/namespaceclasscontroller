# 07: An invalid class

Some mistakes in a class are rejected by the API server when the class is saved: the CRD schema and
its CEL rules check them. Such a class is never stored, so the controller never sees it. The
controller finds the other mistakes: CEL rules can only read `metadata.name` and
`metadata.generateName` of an item, and only the cluster knows whether a kind exists and whether it
is namespaced. Such a class is stored, but in every namespace that uses it the run stops before it
writes anything, and the controller records `InvalidClass` on the namespace and on the class. The
event lists every problem of the class at once.

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

team-a uses team-baseline as in the
[sample](../../config/samples/namespaceclass_v1alpha1_team-baseline.yaml). The ResourceQuota
`compute` in team-a allows 20 pods.

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
apiVersion: v1
kind: ResourceQuota
metadata:
  name: compute
  namespace: team-a
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

## Action

The admin edits team-baseline. The edit raises the pod limit to 30, which is valid. It also adds
two items that a class cannot have: a ClusterRole, which is cluster-scoped, and the ServiceAccount
`default`, which Kubernetes creates in every namespace.

```yaml
apiVersion: namespaceclass.akuity.io/v1alpha1
kind: NamespaceClass
metadata:
  name: team-baseline
spec:
  resources:
    # Items 0 to 3 are the four items of the sample, not repeated here, with one change in the
    # ResourceQuota compute: pods: "30" instead of "20".
    - apiVersion: rbac.authorization.k8s.io/v1   # item 4
      kind: ClusterRole
      metadata:
        name: deployer-reader
      rules:
        - apiGroups: [""]
          resources: ["pods", "services"]
          verbs: ["get", "list", "watch"]
    - apiVersion: v1                             # item 5
      kind: ServiceAccount
      metadata:
        name: default
      imagePullSecrets:
        - name: registry-credentials
```

Save the class above as `team-baseline.yaml`, with items 0 to 3 copied from the
[sample](../../config/samples/namespaceclass_v1alpha1_team-baseline.yaml) and `pods` set to
`"30"` in the ResourceQuota `compute`, and apply it:

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl apply -f team-baseline.yaml
namespaceclass.namespaceclass.akuity.io/team-baseline configured
```

The API server accepts this class: each item has an `apiVersion`, a `kind` and a `metadata.name`.

## What starts a run

The NamespaceClass watch sees an update event. The spec changed, so `metadata.generation` changed,
and the event passes the filter. It maps to every namespace whose label is team-baseline: team-a.

## The run, step by step

1. **Read the namespace**: team-a has the label `namespaceclass.akuity.io/name: team-baseline`. The
   annotation lists four objects.
2. **Previous list**: previous = `{LimitRange/defaults, ResourceQuota/compute,
   ServiceAccount/deployer, RoleBinding.rbac.authorization.k8s.io/deployer-view}`.
3. **Desired objects**: the controller checks every item of the class and finds two problems:
   - `spec.resources[4] (ClusterRole/deployer-reader): kind ClusterRole is cluster-scoped; a class can only create namespaced objects`
   - `spec.resources[5] (ServiceAccount/default): ServiceAccount/default is created by Kubernetes in every namespace and cannot be part of a class`

   Warning `InvalidClass` on team-a and on team-baseline. The run stops here and writes nothing.
   It is not retried, because only an edit of the class can fix it. An edit starts a new run.

## Resulting state

Nothing changed. The ResourceQuota `compute` in team-a still allows 20 pods (same resourceVersion,
`"1180"`): the valid change in the same edit is not applied either. No ClusterRole was created, and
the ServiceAccount `default` of team-a was not touched.

## Events

The warning is recorded about the namespace, with the class as the related object, and about the
class, with the namespace as the related object. A class used by several namespaces gets one event
per namespace.

```console
$ kubectl events -n default --for namespace/team-a
LAST SEEN   TYPE      REASON         OBJECT             MESSAGE
0s          Warning   InvalidClass   Namespace/team-a   NamespaceClass "team-baseline" is invalid, so nothing was changed: spec.resources[4] (ClusterRole/deployer-reader): kind ClusterRole is cluster-scoped; a class can only create namespaced objects; spec.resources[5] (ServiceAccount/default): ServiceAccount/default is created by Kubernetes in every namespace and cannot be part of a class

$ kubectl events -n default --for namespaceclass/team-baseline
LAST SEEN   TYPE      REASON         OBJECT                         MESSAGE
0s          Warning   InvalidClass   NamespaceClass/team-baseline   NamespaceClass "team-baseline" is invalid, so nothing was changed: spec.resources[4] (ClusterRole/deployer-reader): kind ClusterRole is cluster-scoped; a class can only create namespaced objects; spec.resources[5] (ServiceAccount/default): ServiceAccount/default is created by Kubernetes in every namespace and cannot be part of a class
```

## Fixing it

Remove items 4 and 5 and apply the class again. The new generation starts a run. The class is valid,
so the run applies it: the ResourceQuota now allows 30 pods.

```console
$ kubectl events -n default --for namespace/team-a
LAST SEEN   TYPE     REASON    OBJECT             MESSAGE
0s          Normal   Updated   Namespace/team-a   updated ResourceQuota/compute
```

## Which check finds which mistake

Rejected by the API server when the class is saved. kubectl shows the API server's message. The
messages below are from Kubernetes 1.37 (the envtest version):

| Mistake | Message from the API server |
|---|---|
| An item has no `metadata.name` | `spec.resources[4]: Invalid value: metadata.name is required` |
| An item has `metadata.generateName` | `spec.resources[4]: Invalid value: metadata.generateName is not supported; set metadata.name` |
| An item has no `kind` | `spec.resources[4].kind: Required value` |
| An item has no `apiVersion` | `spec.resources[4].apiVersion: Required value` |
| More than 100 items | `spec.resources: Too many: 101: must have at most 100 items` |
| The class name has more than 63 characters | `<nil>: Invalid value: metadata.name must be at most 63 characters because it is used as a label value` |

When the schema check fails (no `kind`, no `apiVersion`, too many items), the message also says that
some validation rules were not checked.

Found by the controller in each run. The run stops with `InvalidClass`, and the note lists every
problem in this form:

| Mistake | Problem in the event note |
|---|---|
| Two items with the same group, kind and name (the version is ignored) | `spec.resources[4] (ServiceAccount/deployer): duplicates spec.resources[0]; group, kind and name must be unique (the version is ignored)` |
| `metadata.namespace` is set | `spec.resources[4] (ConfigMap/team-info): metadata.namespace must be empty, but is "team-a"; the class creates the object in every namespace that uses it` |
| A cluster-scoped kind | `spec.resources[4] (ClusterRole/deployer-reader): kind ClusterRole is cluster-scoped; a class can only create namespaced objects` |
| The ServiceAccount `default` | `spec.resources[5] (ServiceAccount/default): ServiceAccount/default is created by Kubernetes in every namespace and cannot be part of a class` |
| The ConfigMap `kube-root-ca.crt` | `spec.resources[4] (ConfigMap/kube-root-ca.crt): ConfigMap/kube-root-ca.crt is created by Kubernetes in every namespace and cannot be part of a class` |

A kind that the cluster does not serve is not one of these mistakes, because its CRD may be
installed later. It is handled in [use case 08](08-unknown-kind.md). When a class has both a
mistake from the table and an unknown kind, the run records `InvalidClass`.
