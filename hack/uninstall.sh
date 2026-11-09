#!/usr/bin/env bash
# Removes what install.sh added. Platform Mesh itself is left alone.
#   --keep-org      keep the demo organization
#   --keep-knative  keep Knative Eventing and NATS installed
source "$(dirname "$0")/lib.sh"

keep_org=false keep_knative=false
for arg in "$@"; do
  case $arg in
  --keep-org) keep_org=true ;;
  --keep-knative) keep_knative=true ;;
  *) die "unknown option $arg" ;;
  esac
done

source "$ROOT/hack/steps/00-preflight.sh"

log "Removing Subscriptions"
# The controller is still running and removes the Triggers on the way.
kcp root:providers:eventing delete subscriptions --all --timeout=60s >/dev/null 2>&1 || true

# Undo exactly what hack/steps/45-keycloak-reader.sh changed (a stopgap until
# platform-mesh/platform-mesh#578). It has to run before the namespace goes,
# because the ConfigMap with what was changed lives there.
log "Reverting the Keycloak stopgap"
stopgap=$(kind_kubectl -n "$NS" get configmap keycloak-reader-stopgap -o json 2>/dev/null || true)
for realm in $(jq -r '.data // {} | keys[]' <<<"${stopgap:-null}"); do
  mode=$(jq -r --arg r "$realm" '.data[$r]' <<<"$stopgap")
  # The realm may be gone already.
  cfg=$(keycloak GET "/admin/realms/$realm/events/config" 2>/dev/null) || continue
  case $mode in
  # The expiration stays, it does no harm.
  enabled) new=$(jq -c '.eventsEnabled = false' <<<"$cfg") ;;
  added-login) new=$(jq -c '.enabledEventTypes -= ["LOGIN"]' <<<"$cfg") ;;
  *) continue ;;
  esac
  keycloak PUT "/admin/realms/$realm/events/config" -o /dev/null -d "$new"
done
# Deleting the client drops its role mappings.
reader_uuid=$(keycloak GET "/admin/realms/master/clients?clientId=contrib-eventing-keycloak-reader" | jq -r '.[0].id // empty')
[[ -z $reader_uuid ]] || keycloak DELETE "/admin/realms/master/clients/$reader_uuid" -o /dev/null
# The client for hack/login.sh, if the demo org is still there.
login_uuid=$(keycloak GET "/admin/realms/$ORG/clients?clientId=contrib-eventing-login-test" 2>/dev/null | jq -r '.[0].id // empty' || true)
[[ -z $login_uuid ]] || keycloak DELETE "/admin/realms/$ORG/clients/$login_uuid" -o /dev/null

log "Removing the components and the broker"
kind_kubectl delete ns "$NS" eventing-demo-sink --ignore-not-found --wait=false >/dev/null
kind_kubectl delete clusterrolebinding contrib-eventing-ingress-auth-delegator --ignore-not-found >/dev/null
kind_kubectl -n knative-eventing delete networkpolicy contrib-eventing-broker-ingress contrib-eventing-channel contrib-eventing-broker-filter --ignore-not-found >/dev/null 2>&1 || true

# The provider connections have to go before the export. The platform-mesh-operator
# stops at the first connection it cannot handle.
log "Removing the eventing API from kcp"
for secret in contrib-eventing-kcp contrib-eventing-core; do
  index=$(kind_kubectl -n platform-mesh-system get platformmesh platform-mesh -o json |
    jq --arg s "$secret" '(.spec.kcp.extraProviderConnections // []) | map(.secret) | index($s) // empty')
  if [[ -n $index ]]; then
    kind_kubectl -n platform-mesh-system patch platformmesh platform-mesh --type json \
      -p "[{\"op\": \"remove\", \"path\": \"/spec/kcp/extraProviderConnections/$index\"}]" >/dev/null
  fi
done
kcp root:providers delete workspace eventing --ignore-not-found >/dev/null

if ! $keep_org; then
  log "Removing demo organization $ORG"
  kcp root:orgs delete account "$ORG" --ignore-not-found --wait=false >/dev/null
fi

if ! $keep_knative; then
  # The broker's channel has a finalizer that the JetStream controller removes,
  # so the namespace has to be gone before Knative goes.
  kind_kubectl wait --for=delete ns/"$NS" --timeout=300s >/dev/null 2>&1 || true
  log "Removing Knative Eventing"
  dir=$SECRET_DIR/knative-$KNATIVE_VERSION
  # in-memory-channel.yaml is from older installs.
  for f in mt-channel-broker.yaml eventing-jsm.yaml in-memory-channel.yaml eventing-core.yaml eventing-crds.yaml; do
    [[ -f $dir/$f ]] && kind_kubectl delete -f "$dir/$f" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  done
  log "Removing NATS"
  kind_kubectl delete -f "$ROOT/config/nats/nats.yaml" --ignore-not-found --wait=false >/dev/null
fi

# Wait until everything is really gone, so install.sh can run right after.
log "Waiting for the deletions to finish"
kind_kubectl wait --for=delete ns/"$NS" ns/eventing-demo-sink ns/knative-eventing ns/nats-io --timeout=300s >/dev/null 2>&1 || true
if ! $keep_org; then
  org_gone() { ! kcp root:orgs get workspace "$ORG" >/dev/null 2>&1; }
  wait_for "workspace root:orgs:$ORG to be deleted" 300 org_gone
fi
