# 14: A namespace that is being deleted

When a namespace is being deleted (it has a deletion timestamp), the controller does nothing in it.
Every run for that namespace ends right after it reads the namespace: no apply, no delete, no
annotation write, no event. Kubernetes deletes everything in the namespace, including the objects
the controller created, and then the namespace itself.

> Run the commands on this page only in the demo kind cluster. Create it as in
> [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

## Starting state

billing uses internal-network ([sample](../../config/samples/namespace_billing.yaml)) and has its
three objects.

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

## Action

```console
$ KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig kubectl delete namespace billing
namespace "billing" deleted
```

## What starts a run

- The Namespace watch sees an update event: the deletion timestamp was set. The label did not
  change, so the event does not pass the filter. No run starts from it.
- Kubernetes deletes the objects in billing. The drift watches see the deletions of the three
  objects of the controller, and these events start runs for billing.
- An edit of internal-network while billing still exists also starts a run for billing.
- When billing is gone, its delete event does not pass the filter.

## The run, step by step

1. **Read the namespace**: billing has a deletion timestamp. The run ends here.

If billing is already gone, the read returns "not found" and the run ends in the same way. No
previous list, no desired objects, no event.

A namespace can also start terminating while a run is in progress. Then the API server refuses to
create new objects in it. The run notices this refusal and ends without an error and without an
`ApplyFailed` event, so it is not retried.

## Resulting state

Kubernetes deletes the ConfigMap `network-info`, the NetworkPolicies `ingress` and `egress`, and
then billing. The controller did not re-create any of them. internal-network and the other
namespaces that use it are not affected.

## Events

The controller records no event for billing. `kubectl events -n default --for namespace/billing`
shows only older events, if any.
