# 13: Changes made while the controller was stopped

The controller keeps no state of its own. Everything it needs is in the cluster: the label of each
namespace (what it should have), the classes, and the annotation of each namespace (what the
controller created there). When the controller starts, its cache lists every namespace, and each one
arrives as a create event. Every namespace with the class label or the annotation passes the filter
and gets a run. So changes made while the controller was stopped are handled at startup, by the
same runs as always.

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

The three sample classes exist. Four namespaces use them, as at the end of step 8 of the demo:

| Namespace | Class | Objects the controller created |
|---|---|---|
| web-portal | internal-network | NetworkPolicies `ingress` and `egress`, ConfigMap `network-info` |
| billing | internal-network | NetworkPolicies `ingress` and `egress`, ConfigMap `network-info` |
| legacy | internal-network | NetworkPolicies `ingress` and `egress` (the ConfigMap `network-info` is the user's, see [use case 10](10-existing-object.md)) |
| team-a | team-baseline | ServiceAccount `deployer`, ResourceQuota `compute`, LimitRange `defaults`, RoleBinding `deployer-view` |

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
kind: Namespace
metadata:
  name: legacy
  labels:
    namespaceclass.akuity.io/name: internal-network
  annotations:
    namespaceclass.akuity.io/managed-resources: '[{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"egress"},{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]'
---
apiVersion: v1
kind: Namespace
metadata:
  name: team-a
  labels:
    namespaceclass.akuity.io/name: team-baseline
  annotations:
    namespaceclass.akuity.io/managed-resources: '[{"kind":"LimitRange","name":"defaults"},{"kind":"ResourceQuota","name":"compute"},{"kind":"ServiceAccount","name":"deployer"},{"group":"rbac.authorization.k8s.io","kind":"RoleBinding","name":"deployer-view"}]'
```

## Action

Stop the controller, make four changes, and start it again:

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl scale deployment namespaceclass-controller-manager -n namespaceclass-system --replicas=0
deployment.apps/namespaceclass-controller-manager scaled

$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl label namespace web-portal namespaceclass.akuity.io/name-
namespace/web-portal unlabeled

$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl label namespace billing namespaceclass.akuity.io/name=public-network --overwrite
namespace/billing labeled

$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete networkpolicy egress -n legacy
networkpolicy.networking.k8s.io "egress" deleted from legacy namespace

$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete namespaceclass team-baseline
namespaceclass.namespaceclass.akuity.io "team-baseline" deleted

$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl scale deployment namespaceclass-controller-manager -n namespaceclass-system --replicas=1
deployment.apps/namespaceclass-controller-manager scaled
```

While the controller is stopped, nothing restores `egress` in legacy. The garbage collector is part
of Kubernetes, not of the controller, so it still deletes the four objects in team-a when
team-baseline is deleted.

## What starts a run

At startup:

- The Namespace watch lists every namespace, and each one arrives as a create event. web-portal
  passes the filter because it still has the annotation (it no longer has the label). billing,
  legacy and team-a pass because they have the label. Namespaces without the label and the
  annotation, such as `default`, do not pass.
- The NamespaceClass watch lists every class, and each one arrives as a create event. Each maps to
  the namespaces whose label names it: public-network to billing, internal-network to legacy. A
  namespace that is already waiting for a run is not added twice.

## The run, step by step

Each namespace gets the same run as always. The run reads the namespace and the previous list, and
works out the desired objects from the label as it is now:

**web-portal** (label removed): previous = `{ConfigMap/network-info,
NetworkPolicy.networking.k8s.io/egress, NetworkPolicy.networking.k8s.io/ingress}`, desired = `{}`.
The run deletes the three objects (`Deleted`) and removes the annotation, as in
[use case 04](04-opt-out.md).

**billing** (switched to public-network): previous = `{ConfigMap/network-info,
NetworkPolicy.networking.k8s.io/egress, NetworkPolicy.networking.k8s.io/ingress}`, desired =
`{NetworkPolicy.networking.k8s.io/ingress}` from public-network. The run updates `ingress` in place
(`Updated`), then deletes `network-info` and `egress` (`Deleted`), and writes the annotation with
only `ingress`, as in [use case 02](02-switch-class.md).

**legacy** (a managed object deleted): previous = `{NetworkPolicy.networking.k8s.io/egress,
NetworkPolicy.networking.k8s.io/ingress}`, desired = the three objects of internal-network. Live
read: `ingress` is managed, `egress` is missing (new), the user's `network-info` is a conflict
(`Conflict`). The list does not change, so nothing is written to the annotation. The run creates
`egress` again (`Created`).

**team-a** (class deleted): the class team-baseline does not exist. The run records `ClassNotFound`
and writes nothing, as in [use case 05](05-delete-class.md). The annotation still lists the four
objects that the garbage collector deleted.

Result: right after startup, the drift watches are new. A run that has a desired kind whose watch
has not finished its first list ends with `RequeueAfter: 1s`. So billing and legacy usually run once
more, about a second later. Those runs change nothing; legacy records its `Conflict` warning again.
web-portal (nothing desired) and team-a (stopped at `ClassNotFound`) do not repeat.

## Resulting state

| Namespace | After startup |
|---|---|
| web-portal | No objects of the controller, no annotation. |
| billing | `ingress` updated in place (same UID, cidr `0.0.0.0/0`, label and owner reference now public-network); `egress` and `network-info` deleted; annotation `[{"group":"networking.k8s.io","kind":"NetworkPolicy","name":"ingress"}]`. |
| legacy | `egress` exists again, with a new UID. The user's `network-info` is unchanged. Annotation unchanged. |
| team-a | No objects (deleted by the garbage collector). Label and annotation unchanged. |

## Events

Only the events recorded after the restart are shown. Ages and counts depend on timing.

```console
$ kubectl events -n default --for namespace/web-portal
LAST SEEN   TYPE     REASON    OBJECT                 MESSAGE
2s          Normal   Deleted   Namespace/web-portal   deleted ConfigMap/network-info
2s          Normal   Deleted   Namespace/web-portal   deleted NetworkPolicy.networking.k8s.io/egress
2s          Normal   Deleted   Namespace/web-portal   deleted NetworkPolicy.networking.k8s.io/ingress

$ kubectl events -n default --for namespace/billing
LAST SEEN   TYPE     REASON    OBJECT              MESSAGE
2s          Normal   Updated   Namespace/billing   updated NetworkPolicy.networking.k8s.io/ingress
2s          Normal   Deleted   Namespace/billing   deleted ConfigMap/network-info
2s          Normal   Deleted   Namespace/billing   deleted NetworkPolicy.networking.k8s.io/egress

$ kubectl events -n default --for namespace/legacy
LAST SEEN         TYPE      REASON     OBJECT             MESSAGE
2s                Normal    Created    Namespace/legacy   created NetworkPolicy.networking.k8s.io/egress
1s (x2 over 2s)   Warning   Conflict   Namespace/legacy   ConfigMap/network-info already exists and was not created by namespaceclass-controller; it is left unchanged

$ kubectl events -n default --for namespace/team-a
LAST SEEN   TYPE      REASON          OBJECT             MESSAGE
2s          Warning   ClassNotFound   Namespace/team-a   NamespaceClass "team-baseline" does not exist; nothing was changed
```

In legacy, the `Conflict` warning usually comes twice: the first run is repeated about one second
later while the drift watches finish their first list, and legacy did not change in between, so the
two warnings are merged.
