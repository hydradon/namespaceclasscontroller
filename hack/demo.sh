#!/usr/bin/env bash
# Runs steps 1 to 10 of docs/demo.md against a kind cluster where the controller is deployed:
#
#   make kind-deploy KIND_CLUSTER=namespaceclass-demo
#   export KUBECONFIG=$PWD/bin/kind-namespaceclass-demo.kubeconfig
#   hack/demo.sh
#
# The script prints each command and runs it when you press Enter. NONINTERACTIVE=1 runs the
# commands without waiting for Enter. WAIT_TIMEOUT (seconds, default 120) limits each wait for the
# controller. The script refuses to run unless the current context of KUBECONFIG is a kind-*
# context.
set -euo pipefail

# Relative paths in KUBECONFIG are made absolute before the script changes directory, so that
# kubectl and the commands this script suggests use the same file from any directory.
if [[ -n "${KUBECONFIG:-}" ]]; then
  absolute=""
  IFS=: read -r -a parts <<<"$KUBECONFIG"
  for part in "${parts[@]}"; do
    [[ -z "$part" || "$part" == /* ]] || part="$PWD/$part"
    absolute="${absolute:+$absolute:}$part"
  done
  KUBECONFIG=$absolute
fi

cd "$(dirname "$0")/.."

NONINTERACTIVE=${NONINTERACTIVE:-0}
WAIT_TIMEOUT=${WAIT_TIMEOUT:-120}

step() {
  printf '\n==> Step %s: %s\n' "$1" "$2"
  shift 2
  printf '    %s\n' "$@"
  echo
}

run() {
  local cmd=$1 output status=0
  if [[ "$NONINTERACTIVE" == 1 ]]; then
    printf '$ %s\n' "$cmd"
  else
    printf '$ %s' "$cmd"
    read -r _ || true
  fi
  # The output is captured so that it always ends with one newline, also for -o jsonpath.
  output=$(eval "$cmd" 2>&1) || status=$?
  if [[ -n "$output" ]]; then
    printf '%s\n' "$output"
  fi
  echo
  if ((status != 0)); then
    echo "The command failed with exit status $status." >&2
    exit "$status"
  fi
}

# wait_until runs the check every second until it succeeds. The controller and the garbage
# collector work in the background, so the commands that show their work wait for them first.
wait_until() {
  local what=$1 deadline=$((SECONDS + WAIT_TIMEOUT))
  shift
  printf '(waiting until %s)\n\n' "$what"
  until "$@" >/dev/null 2>&1; do
    if ((SECONDS >= deadline)); then
      echo "Timed out after ${WAIT_TIMEOUT}s waiting until $what." >&2
      exit 1
    fi
    sleep 1
  done
}

# managed_objects_are <namespace> <resource types> [Kind/name...] succeeds when the objects of these
# types in the namespace that have the controller label are exactly the given ones.
managed_objects_are() {
  local namespace=$1 types=$2 found
  shift 2
  found=$(kubectl get "$types" -n "$namespace" -l namespaceclass.akuity.io/class \
    -o jsonpath='{range .items[*]}{.kind}/{.metadata.name}{"\n"}{end}') || return 1
  [[ "$(printf '%s\n' "$found" | sort | paste -sd ' ' -)" == "$(printf '%s\n' "$@" | sort | paste -sd ' ' -)" ]]
}

# class_has_no_objects <class> <resource types> succeeds when no object of these types in any
# namespace has the controller label with this class.
class_has_no_objects() {
  local found
  found=$(kubectl get "$2" -A -l "namespaceclass.akuity.io/class=$1" -o name) || return 1
  [[ -z "$found" ]]
}

# field_is <resource> <namespace, empty for a cluster-scoped object> <JSONPath> <value>
field_is() {
  local value
  value=$(kubectl get "$1" ${2:+-n "$2"} -o "jsonpath=$3") || return 1
  [[ "$value" == "$4" ]]
}

# uid_changed <resource> <namespace> <old UID> succeeds when the object exists with another UID.
uid_changed() {
  local uid
  uid=$(kubectl get "$1" -n "$2" -o 'jsonpath={.metadata.uid}') || return 1
  [[ -n "$uid" && "$uid" != "$3" ]]
}

# has_event <namespace> <reason> succeeds when an event with this reason is recorded about the
# Namespace.
has_event() {
  local found
  found=$(kubectl get events -n default -o name \
    --field-selector "involvedObject.kind=Namespace,involvedObject.name=$1,reason=$2") || return 1
  [[ -n "$found" ]]
}

check_cluster() {
  if [[ -z "${KUBECONFIG:-}" ]]; then
    echo "KUBECONFIG is not set. Point it at the kubeconfig file of the kind cluster, for example:" >&2
    echo "  export KUBECONFIG=\$PWD/bin/kind-namespaceclass-demo.kubeconfig" >&2
    exit 1
  fi
  context=$(kubectl config current-context 2>/dev/null) || context=""
  if [[ "$context" != kind-* ]]; then
    echo "The current context of KUBECONFIG is \"$context\", which is not a kind-* context. Refusing to run." >&2
    exit 1
  fi

  local deploy="make kind-deploy KIND_CLUSTER=${context#kind-}"
  if ! kubectl wait --for=condition=Established crd/namespaceclasses.namespaceclass.akuity.io --timeout=60s >/dev/null; then
    echo "The NamespaceClass CRD is not installed. Deploy the controller first: $deploy" >&2
    exit 1
  fi
  if ! kubectl rollout status deployment/namespaceclass-controller-manager -n namespaceclass-system --timeout="${WAIT_TIMEOUT}s" >/dev/null; then
    echo "The controller is not running. Deploy it first: $deploy" >&2
    exit 1
  fi

  local leftovers
  leftovers=$(kubectl get namespace web-portal billing team-a legacy --ignore-not-found -o name)
  leftovers+=$(kubectl get namespaceclass public-network internal-network team-baseline --ignore-not-found -o name)
  if [[ -n "$leftovers" ]]; then
    # The commands name the kubeconfig, because the shell they are pasted into may use another one.
    local kubectl_cmd
    printf -v kubectl_cmd 'KUBECONFIG=%q kubectl' "$KUBECONFIG"
    echo "The cluster has objects from an earlier run of the demo. Delete them, then run the demo again:" >&2
    echo "  $kubectl_cmd delete namespace web-portal billing team-a legacy --ignore-not-found" >&2
    echo "  $kubectl_cmd delete namespaceclass public-network internal-network team-baseline --ignore-not-found" >&2
    exit 1
  fi
}

check_cluster
echo "NamespaceClass demo on the kind cluster ${context#kind-}. The controller runs in namespaceclass-system:"
echo
run "kubectl get pods -n namespaceclass-system"

step 1 "Create the classes" \
  "A NamespaceClass lists normal Kubernetes manifests. public-network has one NetworkPolicy" \
  "that allows ingress from any address. internal-network allows ingress only from the VPN range" \
  "10.8.0.0/16, allows egress only to the VPN range and to DNS, and adds a ConfigMap."
run "cat config/samples/namespaceclass_v1alpha1_public-network.yaml"
run "kubectl apply -f config/samples/namespaceclass_v1alpha1_public-network.yaml -f config/samples/namespaceclass_v1alpha1_internal-network.yaml"
run "kubectl get nsclass"

step 2 "Use a class" \
  "A namespace uses a class through the label namespaceclass.akuity.io/name. The controller" \
  "creates the objects of the class in the namespace, marks them with the label" \
  "namespaceclass.akuity.io/class and an owner reference to the class, and lists them in an" \
  "annotation on the namespace."
run "kubectl apply -f config/samples/namespace_web-portal.yaml"
wait_until "the controller has created NetworkPolicy ingress in web-portal" \
  managed_objects_are web-portal networkpolicy,configmap NetworkPolicy/ingress
run "kubectl get networkpolicy -n web-portal"
run "kubectl get networkpolicy ingress -n web-portal -o yaml"
run "kubectl get namespace web-portal -o jsonpath='{.metadata.annotations.namespaceclass\.akuity\.io/managed-resources}'"
wait_until "the Created event is stored" has_event web-portal Created
run "kubectl events -n default --for namespace/web-portal"

step 3 "Switch to another class" \
  "Both classes define the NetworkPolicy ingress, so the controller updates it in place: it keeps" \
  "its UID, and the allowed range changes to the VPN range. egress and network-info are new."
ingress_uid=$(kubectl get networkpolicy ingress -n web-portal -o 'jsonpath={.metadata.uid}')
run "kubectl get networkpolicy ingress -n web-portal -o jsonpath='{.metadata.uid}'"
run "kubectl label namespace web-portal namespaceclass.akuity.io/name=internal-network --overwrite"
wait_until "the controller has applied internal-network in web-portal" \
  managed_objects_are web-portal networkpolicy,configmap NetworkPolicy/ingress NetworkPolicy/egress ConfigMap/network-info
wait_until "NetworkPolicy ingress allows the VPN range" \
  field_is networkpolicy/ingress web-portal '{.spec.ingress[0].from[0].ipBlock.cidr}' 10.8.0.0/16
run "kubectl get networkpolicy,configmap -n web-portal -L namespaceclass.akuity.io/class"
run "kubectl get networkpolicy ingress -n web-portal -o jsonpath='{.metadata.uid} {.spec.ingress[0].from[0].ipBlock.cidr}'"
if ! field_is networkpolicy/ingress web-portal '{.metadata.uid}' "$ingress_uid"; then
  echo "NetworkPolicy ingress was created again instead of being updated in place." >&2
  exit 1
fi

step 4 "Update a class" \
  "Every namespace that uses a class follows changes to the class. Version 2 of internal-network" \
  "changes the VPN range to 10.9.0.0/16 and adds the NetworkPolicy allow-monitoring. Applying the" \
  "original class again deletes allow-monitoring."
run "kubectl apply -f config/samples/namespace_billing.yaml"
wait_until "the controller has applied internal-network in billing" \
  managed_objects_are billing networkpolicy,configmap NetworkPolicy/ingress NetworkPolicy/egress ConfigMap/network-info
run "kubectl apply -f docs/demo/internal-network-v2.yaml"
for ns in web-portal billing; do
  wait_until "the controller has applied version 2 in $ns" \
    managed_objects_are "$ns" networkpolicy,configmap NetworkPolicy/ingress NetworkPolicy/egress NetworkPolicy/allow-monitoring ConfigMap/network-info
  wait_until "NetworkPolicy ingress in $ns allows 10.9.0.0/16" \
    field_is networkpolicy/ingress "$ns" '{.spec.ingress[0].from[0].ipBlock.cidr}' 10.9.0.0/16
done
run "kubectl get networkpolicy -A -l namespaceclass.akuity.io/class=internal-network"
run "kubectl get networkpolicy ingress -n billing -o jsonpath='{.spec.ingress[0].from[0].ipBlock.cidr}'"
run "kubectl apply -f config/samples/namespaceclass_v1alpha1_internal-network.yaml"
for ns in web-portal billing; do
  wait_until "the controller has applied the original class in $ns" \
    managed_objects_are "$ns" networkpolicy,configmap NetworkPolicy/ingress NetworkPolicy/egress ConfigMap/network-info
  wait_until "NetworkPolicy ingress in $ns allows 10.8.0.0/16" \
    field_is networkpolicy/ingress "$ns" '{.spec.ingress[0].from[0].ipBlock.cidr}' 10.8.0.0/16
done
run "kubectl get networkpolicy -A -l namespaceclass.akuity.io/class=internal-network"

step 5 "Undo changes made by hand" \
  "The controller watches the objects it created. It creates a deleted object again and puts an" \
  "edited field back, usually within a second."
egress_uid=$(kubectl get networkpolicy egress -n web-portal -o 'jsonpath={.metadata.uid}')
run "kubectl delete networkpolicy egress -n web-portal"
wait_until "the controller has created NetworkPolicy egress again" \
  uid_changed networkpolicy/egress web-portal "$egress_uid"
run "kubectl get networkpolicy egress -n web-portal"
run "kubectl patch networkpolicy ingress -n web-portal --type=merge -p '{\"spec\":{\"ingress\":[]}}'"
wait_until "the controller has put the ingress rule back" \
  field_is networkpolicy/ingress web-portal '{.spec.ingress[0].from[0].ipBlock.cidr}' 10.8.0.0/16
run "kubectl get networkpolicy ingress -n web-portal -o jsonpath='{.spec.ingress[0].from[0].ipBlock.cidr}'"

step 6 "Leave existing objects alone" \
  "The namespace legacy already has a ConfigMap network-info that the controller did not create." \
  "The controller leaves it unchanged, records a Conflict warning and creates the other objects."
run "kubectl create namespace legacy"
run "kubectl create configmap network-info -n legacy --from-literal=owner=me"
run "kubectl label namespace legacy namespaceclass.akuity.io/name=internal-network"
wait_until "the controller has created the NetworkPolicies in legacy" \
  managed_objects_are legacy networkpolicy,configmap NetworkPolicy/ingress NetworkPolicy/egress
wait_until "the Conflict event is stored" has_event legacy Conflict
run "kubectl events -n default --for namespace/legacy"
run "kubectl get configmap network-info -n legacy -o yaml"
run "kubectl get namespace legacy -o jsonpath='{.metadata.annotations.namespaceclass\.akuity\.io/managed-resources}'"

step 7 "Any kind of object" \
  "team-baseline creates a ServiceAccount, a ResourceQuota, a LimitRange and a RoleBinding."
run "kubectl apply -f config/samples/namespaceclass_v1alpha1_team-baseline.yaml"
run "kubectl apply -f config/samples/namespace_team-a.yaml"
wait_until "the controller has created the objects of team-baseline in team-a" \
  managed_objects_are team-a serviceaccount,resourcequota,limitrange,rolebinding \
  ServiceAccount/deployer ResourceQuota/compute LimitRange/defaults RoleBinding/deployer-view
run "kubectl get serviceaccount,resourcequota,limitrange,rolebinding -n team-a -l namespaceclass.akuity.io/class=team-baseline"

step 8 "A typo in the label" \
  "A label that names a class that does not exist changes nothing. Only removing the label" \
  "deletes objects, so a typo cannot delete the objects of a namespace."
run "kubectl label namespace team-a namespaceclass.akuity.io/name=team-baselin --overwrite"
wait_until "the ClassNotFound event is stored" has_event team-a ClassNotFound
run "kubectl events -n default --for namespace/team-a"
run "kubectl get serviceaccount,resourcequota,limitrange,rolebinding -n team-a -l namespaceclass.akuity.io/class=team-baseline"
run "kubectl label namespace team-a namespaceclass.akuity.io/name=team-baseline --overwrite"

step 9 "Opt out" \
  "Removing the label deletes every object the controller created in the namespace and removes" \
  "the annotation. Objects that the controller did not create stay."
run "kubectl label namespace web-portal namespaceclass.akuity.io/name-"
wait_until "the controller has deleted its objects in web-portal" \
  managed_objects_are web-portal networkpolicy,configmap
wait_until "the controller has removed the annotation" \
  field_is namespace/web-portal "" '{.metadata.annotations.namespaceclass\.akuity\.io/managed-resources}' ""
run "kubectl get networkpolicy,configmap -n web-portal"
run "kubectl get namespace web-portal -o jsonpath='{.metadata.annotations.namespaceclass\.akuity\.io/managed-resources}'"

step 10 "Delete a class" \
  "Each created object has an owner reference to its class, so the Kubernetes garbage collector" \
  "deletes the objects in every namespace when the class is deleted. The controller does not" \
  "delete them itself. The first deletion after the CRD is installed can take about 30 seconds."
run "kubectl get networkpolicy,configmap -A -l namespaceclass.akuity.io/class=internal-network"
run "kubectl delete namespaceclass internal-network"
wait_until "the garbage collector has deleted the objects of internal-network" \
  class_has_no_objects internal-network networkpolicy,configmap
run "kubectl get networkpolicy,configmap -A -l namespaceclass.akuity.io/class=internal-network"
run "kubectl get configmap network-info -n legacy"
wait_until "the ClassNotFound event is stored" has_event legacy ClassNotFound
run "kubectl events -n default --for namespace/legacy"

echo "The demo is finished. To delete the cluster: make cleanup-test-e2e KIND_CLUSTER=${context#kind-}"
