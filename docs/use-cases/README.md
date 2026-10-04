# Use cases

One page per situation that the NamespaceClass controller handles. Each page shows concrete objects
before and after, and what the controller does in each step of the run. The pages use the sample
classes and namespaces of [`config/samples/`](../../config/samples/) and the
[demo guide](../demo.md) where they fit.

> The pages show commands that create, change and delete objects. Run them only in the demo kind
> cluster: create it as in [step 0 of the demo](../demo.md#step-0-set-up), and first run
> `export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig` in the repository root. Every
> command that changes the cluster also starts with
> `KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig`, so it goes to the demo cluster even
> without the `export`.

| # | Use case | Summary |
|---|---|---|
| 01 | [A namespace starts using a class](01-opt-in.md) | The objects of the class are created in the namespace and listed in its annotation. |
| 02 | [Switching to another class](02-switch-class.md) | The new class is applied first; an object both classes define is updated in place (same UID); then the objects only the old class had are deleted. |
| 03 | [Editing a class](03-edit-class.md) | Every namespace of the class gets added, changed and removed items; a removed field leaves the live object; unchanged items get no event. |
| 04 | [Removing the label (opt out)](04-opt-out.md) | Every object the controller created in the namespace is deleted, and the annotation is removed. |
| 05 | [Deleting a class](05-delete-class.md) | The garbage collector deletes the objects (or keeps them with `--cascade=orphan`); labeled namespaces get `ClassNotFound`; creating the class again creates the objects again, or points the owner references of kept objects to the new class. |
| 06 | [The label names no usable class](06-missing-class.md) | A typo, an empty value or a class that is being deleted: nothing is written, `ClassNotFound`. |
| 07 | [An invalid class](07-invalid-class.md) | Some mistakes are rejected when the class is saved; the others stop every run with `InvalidClass` on the namespace and the class. |
| 08 | [A kind that is not installed yet](08-unknown-kind.md) | Nothing is applied, `ApplyFailed`, retries with backoff; after the CRD is installed, the whole class is applied. |
| 09 | [The API server rejects one object](09-rejected-item.md) | The other objects are applied, nothing is deleted, `ApplyFailed` with the API server's message, retries; a rename fixes a field that cannot change. |
| 10 | [An object with the same name already exists](10-existing-object.md) | The existing object is left unchanged, `Conflict`, no retry, never listed; the rest of the class is applied as usual. |
| 11 | [Someone edits or deletes a created object](11-drift.md) | The drift watch starts a run that restores the object; runs repeat every second until a new watch has listed its objects (at most 30 seconds). |
| 12 | [Someone removes the controller's label from an object](12-label-removed-from-object.md) | The owner reference still marks the object as managed, so the label is put back; when both are removed, the controller no longer manages the object. |
| 13 | [Changes made while the controller was stopped](13-controller-restart.md) | At startup every namespace with the label or the annotation gets a run, which handles the changes. |
| 14 | [A namespace that is being deleted](14-namespace-terminating.md) | The controller does nothing; Kubernetes deletes the objects with the namespace. |
| 15 | [The managed-resources annotation cannot be read](15-unreadable-annotation.md) | `InvalidAnnotation`; the list counts as empty and nothing is deleted; the next run that gets past the class checks writes the annotation again. |

## How to read these pages

Each page has these sections, in this order. A page with several actions repeats the sections from
Action to Events for each action.

1. **A short summary** of what happens and why.
2. **Starting state**: the objects before the action, as YAML. Only the fields that matter are
   shown. Fields such as `creationTimestamp`, `managedFields`, `status` and the label
   `kubernetes.io/metadata.name` (which Kubernetes adds to every namespace) are left out. UIDs and
   resource versions are examples; they are shown where they matter, for example to show that an
   object was updated in place.
3. **Action**: the commands the user runs.
4. **What starts a run**: which watch sees which event, and why the event passes its filter (or why
   no run starts). The watches and filters are described in the README under
   [What starts a run](../../README.md#what-starts-a-run).
5. **The run, step by step**, in the order of the code (see [One run](../../README.md#one-run) in the
   README):
   1. Read the namespace from the API server. Stop if it is being deleted.
   2. Previous list: read the annotation.
   3. Desired objects: build them from the class, or stop with `ClassNotFound`, `InvalidClass` or
      `ApplyFailed` (a kind that is not installed) before anything is written.
   4. Drift watches: add a watch for each new kind.
   5. Live read and classify each desired object: new, managed, or conflict.
   6. Record the old and the new objects: write previous ∪ (desired − conflicts) to the annotation,
      unless it already holds that list.
   7. Apply each object that is not a conflict with Server-Side Apply: `Created`, `Updated`, no
      event when nothing changed, or `ApplyFailed`.
   8. Delete old objects: delete previous − desired, only if no item failed.
   9. Record the current objects: write (desired − conflicts) ∪ failed deletions, or remove the
      annotation when this list is empty.
   10. Result: done, an error (retried with backoff from 5 milliseconds up to 5 minutes), or
       `RequeueAfter: 1s` while a new drift watch has not finished its first list.
6. **Resulting state**: what changed, with a short list of the differences.
7. **Events**: what `kubectl events -n default --for namespace/<name>` shows. Events about
   namespaces and classes are stored in the `default` namespace, because both are cluster-scoped.

Conventions:

- In the run steps, objects are written as in the event notes: `Kind/name` for the core group, and
  `Kind.group/name` for other groups, for example `NetworkPolicy.networking.k8s.io/ingress`.
- "previous" is the list read from the annotation, "desired" is the list of objects the class wants,
  and "conflicts" are desired objects that exist but were not created by the controller.
- The annotation is shown exactly as the controller writes it: a JSON list sorted by group, kind and
  name, with the group left out for the core group.
- "Managed" means that an object has the label `namespaceclass.akuity.io/class` or an owner
  reference to a NamespaceClass. The controller only changes or deletes managed objects.
- When the controller updates or deletes one of its objects, the drift watch of that kind also sees
  the change and starts one more run for the namespace. That run finds nothing to change and writes
  nothing. Creations start no run, because the drift watches ignore create events. The pages mention
  this only where it matters.
- Event lists show the events of the action on the page. Ages and counts such as `(x2 over 1s)`
  depend on timing: identical events that come close together are merged into one event with a
  count. Outputs marked "real output" are copied from the [demo guide](../demo.md).
