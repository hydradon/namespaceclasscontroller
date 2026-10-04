# 06: The label names no usable class

The label `namespaceclass.akuity.io/name` can name a class that does not exist (for example a typo),
have an empty value, or name a class that is being deleted. In all three cases the controller writes
nothing: it does not apply, does not delete, and does not change the annotation. It records a
`ClassNotFound` warning. Only a missing label means "opt out" ([use case 04](04-opt-out.md)), so a
typo never deletes objects. Fixing the label, or creating the class, starts a run that applies the
class.

This page follows [step 8 of the demo](../demo.md#step-8-a-typo-in-the-label).

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

team-a uses team-baseline ([sample](../../config/samples/namespaceclass_v1alpha1_team-baseline.yaml)),
as after step 7 of the demo. No class named team-baselin exists.

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
kind: ServiceAccount
metadata:
  name: deployer
  namespace: team-a
  labels:
    namespaceclass.akuity.io/class: team-baseline
  ownerReferences:
    - apiVersion: namespaceclass.akuity.io/v1alpha1
      kind: NamespaceClass
      name: team-baseline
      uid: 354f2831-2586-42b4-8aa5-135bc6327803
      controller: true
```

The ResourceQuota `compute`, the LimitRange `defaults` and the RoleBinding `deployer-view` have the
same label and owner reference.

## Action

A typo in the label value:

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl label namespace team-a namespaceclass.akuity.io/name=team-baselin --overwrite
namespace/team-a labeled
```

## What starts a run

The Namespace watch sees an update event. It passes the filter because the value of the label
changed from team-baseline to team-baselin.

## The run, step by step

1. **Read the namespace**: team-a has the label `namespaceclass.akuity.io/name: team-baselin`. The
   annotation lists four objects.
2. **Previous list**: previous = `{LimitRange/defaults, ResourceQuota/compute,
   ServiceAccount/deployer, RoleBinding.rbac.authorization.k8s.io/deployer-view}`.
3. **Desired objects**: the class team-baselin does not exist. Warning `ClassNotFound`. The run stops
   here and writes nothing.

The run returns no error, so it is not retried. The next run comes from a change of the label, a
class event for team-baselin (for example the class is created), drift of one of the four objects,
or the periodic resync.

## Resulting state

Nothing changed. The four objects keep their content, their label
`namespaceclass.akuity.io/class: team-baseline` and their owner reference. The annotation still
lists them. Real output from the demo:

```console
$ kubectl get serviceaccount,resourcequota,limitrange,rolebinding -n team-a -l namespaceclass.akuity.io/class=team-baseline
NAME                      AGE
serviceaccount/deployer   0s

NAME                    REQUEST                                                 LIMIT                                    AGE
resourcequota/compute   pods: 0/20, requests.cpu: 0/4, requests.memory: 0/8Gi   limits.cpu: 0/8, limits.memory: 0/16Gi   0s

NAME                  CREATED AT
limitrange/defaults   2026-10-04T20:04:07Z

NAME                                                  ROLE               AGE
rolebinding.rbac.authorization.k8s.io/deployer-view   ClusterRole/view   0s
```

## Events

Real output from the demo. The four `Created` events come from step 7, when team-a started to use
team-baseline.

```console
$ kubectl events -n default --for namespace/team-a
LAST SEEN   TYPE      REASON          OBJECT             MESSAGE
0s          Normal    Created         Namespace/team-a   created ServiceAccount/deployer
0s          Normal    Created         Namespace/team-a   created ResourceQuota/compute
0s          Normal    Created         Namespace/team-a   created LimitRange/defaults
0s          Normal    Created         Namespace/team-a   created RoleBinding.rbac.authorization.k8s.io/deployer-view
0s          Warning   ClassNotFound   Namespace/team-a   NamespaceClass "team-baselin" does not exist; nothing was changed
```

## The other two cases

**Empty value.**
`KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl label namespace team-a namespaceclass.akuity.io/name= --overwrite`
keeps the label with an empty value. The update passes the filter (the value changed), and step 3
stops the run:

```console
LAST SEEN   TYPE      REASON          OBJECT             MESSAGE
0s          Warning   ClassNotFound   Namespace/team-a   label namespaceclass.akuity.io/name is empty; nothing was changed
```

**The class is being deleted.** A class is being deleted when it has a deletion timestamp but still
exists because a finalizer holds it, for example during a deletion with `--cascade=orphan`
([use case 05](05-delete-class.md), part 3). Step 3 finds the class, sees the deletion timestamp
and stops the run:

```console
LAST SEEN   TYPE      REASON          OBJECT             MESSAGE
0s          Warning   ClassNotFound   Namespace/team-a   NamespaceClass "team-baseline" is being deleted; nothing was changed
```

In this time the controller also ignores edits to the class, and does not re-create an object that
someone (or the garbage collector) deletes.

## Fixing it

**Fix the label.**

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl label namespace team-a namespaceclass.akuity.io/name=team-baseline --overwrite
namespace/team-a labeled
```

The update passes the filter (the value changed). The run finds team-baseline. All four objects
exist, are managed and already match the class, so every apply changes nothing. The annotation
already lists the four objects. The run writes nothing and records no event.

**Create the class.** A namespace can also get its label before its class exists. When the class is
created, the NamespaceClass watch sees a create event and maps it to every namespace whose label
names the class. Each one gets a run that applies the class, as in [use case 01](01-opt-in.md).
