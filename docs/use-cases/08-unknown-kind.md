# 08: A kind that is not installed yet

A class can hold custom resources whose CRD is installed later. While the cluster does not serve a
kind of the class, the controller cannot check the class completely. So it applies nothing, not
even the items of known kinds, records `ApplyFailed`, and retries with backoff: the delay starts at
5 milliseconds and doubles after each failed run, up to 5 minutes. The first retry after the CRD is
installed applies the whole class.

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

team-a uses team-baseline as in the
[sample](../../config/samples/namespaceclass_v1alpha1_team-baseline.yaml). cert-manager is not
installed, so the cluster does not serve the kind `Issuer` in `cert-manager.io/v1`.

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: team-a
  labels:
    namespaceclass.akuity.io/name: team-baseline
  annotations:
    namespaceclass.akuity.io/managed-resources: '[{"kind":"LimitRange","name":"defaults"},{"kind":"ResourceQuota","name":"compute"},{"kind":"ServiceAccount","name":"deployer"},{"group":"rbac.authorization.k8s.io","kind":"RoleBinding","name":"deployer-view"}]'
```

The four objects of the class exist in team-a, with the label `namespaceclass.akuity.io/class:
team-baseline` and an owner reference to the class.

## Action

The admin adds a cert-manager `Issuer` to team-baseline:

```yaml
apiVersion: namespaceclass.akuity.io/v1alpha1
kind: NamespaceClass
metadata:
  name: team-baseline
spec:
  resources:
    # Items 0 to 3 are the four items of the sample, not repeated here.
    - apiVersion: cert-manager.io/v1   # item 4, new
      kind: Issuer
      metadata:
        name: selfsigned
      spec:
        selfSigned: {}
```

Save the class above as `team-baseline.yaml`, with items 0 to 3 copied from the
[sample](../../config/samples/namespaceclass_v1alpha1_team-baseline.yaml), and apply it:

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl apply -f team-baseline.yaml
namespaceclass.namespaceclass.akuity.io/team-baseline configured
```

The API server accepts the class. It does not check whether the kind of an item exists.

## What starts a run

The NamespaceClass watch sees an update event with a new `metadata.generation`, which passes the
filter. It maps to team-a. After that, each failed run is retried by the backoff.

## The run, step by step

1. **Read the namespace**: team-a has the label `namespaceclass.akuity.io/name: team-baseline`. The
   annotation lists four objects.
2. **Previous list**: previous = `{LimitRange/defaults, ResourceQuota/compute,
   ServiceAccount/deployer, RoleBinding.rbac.authorization.k8s.io/deployer-view}`.
3. **Desired objects**: the class has no mistake from [use case 07](07-invalid-class.md), but the
   cluster does not serve `Issuer` in `cert-manager.io/v1`. Warning `ApplyFailed`. The run stops
   here and writes nothing. It returns an error, so it is retried with backoff.

Every retry goes the same way until the CRD exists. If the same edit had also changed one of the
other four items, that change would wait too. After about five and a half minutes of failed runs,
the delay reaches its maximum, and the controller tries again every 5 minutes.

## Resulting state

Nothing changed: no Issuer, the four objects are unchanged, and the annotation still lists four
objects.

## Events

```console
$ kubectl events -n default --for namespace/team-a
LAST SEEN         TYPE      REASON        OBJECT             MESSAGE
3s (x2 over 3s)   Warning   ApplyFailed   Namespace/team-a   cannot apply NamespaceClass "team-baseline", so nothing was changed: spec.resources[4] (Issuer/selfsigned): no matches for kind "Issuer" in version "cert-manager.io/v1"
```

The retries record the same warning, so it is merged into one event with a count.

## After the CRD is installed

### Action

Install cert-manager with any method. What matters here is that its CRDs are installed, for example:

```sh
KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl apply -f cert-manager.yaml # the manifest of a cert-manager release
```

### What starts a run

The controller does not watch CRDs, so the installation itself starts no run. The next retry of the
backoff starts it (at most 5 minutes later), or any earlier event for team-a, such as another edit
of the class.

### The run, step by step

1. **Read the namespace**: as before.
2. **Previous list**: the same four objects.
3. **Desired objects**: the controller looks the kind up again and now finds it. desired =
   `{ServiceAccount/deployer, ResourceQuota/compute, LimitRange/defaults,
   RoleBinding.rbac.authorization.k8s.io/deployer-view, Issuer.cert-manager.io/selfsigned}`.
4. **Drift watches**: `Issuer` is new, so the run adds its drift watch. The watch has not listed its
   objects yet.
5. **Live read and classify**: the four objects exist and are managed. The Issuer does not exist, so
   it is new. conflicts = `{}`.
6. **Record the old and the new objects**: the list gains the Issuer. The controller writes
   `[{"kind":"LimitRange","name":"defaults"},{"kind":"ResourceQuota","name":"compute"},{"kind":"ServiceAccount","name":"deployer"},{"group":"cert-manager.io","kind":"Issuer","name":"selfsigned"},{"group":"rbac.authorization.k8s.io","kind":"RoleBinding","name":"deployer-view"}]`
7. **Apply**: the four existing objects do not change and get no event. The Issuer is created. Event
   `Created`.
8. **Delete old objects**: previous − desired = `{}`.
9. **Record the current objects**: the same list as in step 6, so nothing is written.
10. **Result**: the Issuer watch has not finished its first list, so the run returns
    `RequeueAfter: 1s`. The next run changes nothing.

### Resulting state

```yaml
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: selfsigned
  namespace: team-a
  labels:
    namespaceclass.akuity.io/class: team-baseline
  ownerReferences:
    - apiVersion: namespaceclass.akuity.io/v1alpha1
      kind: NamespaceClass
      name: team-baseline
      uid: 354f2831-2586-42b4-8aa5-135bc6327803
      controller: true
spec:
  selfSigned: {}
```

What changed: the Issuer `selfsigned` is new in team-a, and the annotation lists it. The four other
objects did not change.

### Events

```console
$ kubectl events -n default --for namespace/team-a
LAST SEEN   TYPE     REASON    OBJECT             MESSAGE
0s          Normal   Created   Namespace/team-a   created Issuer.cert-manager.io/selfsigned
```
