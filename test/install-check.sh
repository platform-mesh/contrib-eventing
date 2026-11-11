#!/usr/bin/env bash
# Checks that hack/install.sh can run again without changing anything, and with
# --uninstall that hack/uninstall.sh leaves nothing behind and install.sh works
# afterwards. Run it on an installation made by hack/install.sh.
#   --uninstall  also uninstall (keeping the demo org) and install again
source "$(dirname "$0")/../hack/lib.sh"
source "$ROOT/hack/steps/00-preflight.sh" >/dev/null

uninstall=false
for arg in "$@"; do
  case $arg in
  --uninstall) uninstall=true ;;
  *) die "unknown option $arg" ;;
  esac
done

failed=0
pass() { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; failed=1; }

namespaces=("$NS" eventing-demo-sink knative-eventing nats-io)

# What install.sh sets up, without fields the cluster changes on its own.
state() {
  for ns in "${namespaces[@]}"; do
    kind_kubectl -n "$ns" get deploy,statefulset,configmap,secret,service,networkpolicy,serviceaccount -o json |
      jq -S --arg ns "$ns" '.items[] | select(.metadata.name != "kube-root-ca.crt") |
        {ns: $ns, kind, name: .metadata.name, generation: .metadata.generation, spec, data: (.data // null)}'
    kind_kubectl -n "$ns" get pods -o json |
      jq -c --arg ns "$ns" '.items[] | {ns: $ns, pod: .metadata.name, restarts: [.status.containerStatuses[]?.restartCount]}'
  done
  kind_kubectl -n "$NS" get brokers,triggers,natsjetstreamchannels,subscriptions.messaging.knative.dev -o json |
    jq -S '.items[] | {kind, name: .metadata.name, uid: .metadata.uid, generation: .metadata.generation, spec}'
  kcp root:providers:eventing get subscriptions -o json | jq -S '.items[] | {name: .metadata.name, generation: .metadata.generation, spec}'
  kind_kubectl -n nats-io exec nats-0 -- wget -qO- 'http://localhost:8222/jsz?consumers=true' |
    jq -c '.account_details[].stream_detail[] | {name, consumers: [.consumer_detail[].name] | sort}'
  keycloak_state
}

# The Keycloak side of the stopgap (hack/steps/45-keycloak-reader.sh): the
# reader client with its roles on each org realm, and each org realm's event
# store settings.
keycloak_state() {
  local reader sa realm
  reader=$(keycloak GET "/admin/realms/master/clients?clientId=contrib-eventing-keycloak-reader")
  jq -S '[.[] | {clientId, publicClient, serviceAccountsEnabled, standardFlowEnabled, directAccessGrantsEnabled}]' <<<"$reader"
  sa=$(jq -r '.[0].id // empty' <<<"$reader")
  if [[ -n $sa ]]; then
    keycloak GET "/admin/realms/master/clients/$sa/service-account-user" | jq -S '{username, enabled}'
    sa=$(keycloak GET "/admin/realms/master/clients/$sa/service-account-user" | jq -r .id)
    keycloak GET "/admin/realms/master/users/$sa/role-mappings" |
      jq -S '{realm: [.realmMappings // [] | .[].name] | sort, clients: ((.clientMappings // {}) | map_values([.mappings[].name] | sort))}'
  fi
  for realm in $(keycloak GET /admin/realms | jq -r '.[].realm' | sort); do
    [[ $realm == master || $realm == welcome ]] && continue
    keycloak GET "/admin/realms/$realm" | jq -S '{realm, eventsEnabled, enabledEventTypes: (.enabledEventTypes // [] | sort), eventsExpiration}'
  done
}

install() { "$ROOT/hack/install.sh" >"$SECRET_DIR/install-check.log" 2>&1 || die "install.sh failed, see $SECRET_DIR/install-check.log"; }

if $uninstall; then
  # uninstall.sh reads what the stopgap changed from this ConfigMap, which goes
  # with the namespace.
  stopgap=$(kind_kubectl -n "$NS" get configmap keycloak-reader-stopgap -o json | jq -c '.data // {}')
  log "Uninstall leaves nothing behind"
  "$ROOT/hack/uninstall.sh" --keep-org >/dev/null 2>&1 || fail "uninstall.sh failed"
  for ns in "${namespaces[@]}"; do
    kind_kubectl get ns "$ns" >/dev/null 2>&1 && fail "namespace $ns still there" || pass "namespace $ns gone"
  done
  crds=$(kind_kubectl get crd -o name | grep -c 'knative.dev' || true)
  [[ $crds == 0 ]] && pass "no Knative CRDs" || fail "$crds Knative CRDs left"
  pvs=$(kind_kubectl get pv -o json | jq '[.items[] | select(.spec.claimRef.namespace == "nats-io")] | length')
  [[ $pvs == 0 ]] && pass "no NATS volumes" || fail "$pvs NATS volumes left"
  kind_kubectl get clusterrolebinding contrib-eventing-ingress-auth-delegator >/dev/null 2>&1 &&
    fail "ClusterRoleBinding still there" || pass "ClusterRoleBinding gone"
  kcp root:providers get workspace eventing >/dev/null 2>&1 &&
    fail "workspace root:providers:eventing still there" || pass "workspace root:providers:eventing gone"
  connections=$(kind_kubectl -n platform-mesh-system get platformmesh platform-mesh -o json |
    jq '[.spec.kcp.extraProviderConnections // [] | .[] | select(.secret | startswith("contrib-eventing"))] | length')
  [[ $connections == 0 ]] && pass "no provider connections" || fail "$connections provider connections left"

  [[ -z $(keycloak GET "/admin/realms/master/clients?clientId=contrib-eventing-keycloak-reader" | jq -r '.[0].id // empty') ]] &&
    pass "Keycloak reader client gone" || fail "Keycloak reader client still there"
  for realm in $(jq -r 'keys[] | select(. != "contrib-eventing-keycloak-reader")' <<<"$stopgap"); do
    rep=$(keycloak GET "/admin/realms/$realm" 2>/dev/null) || continue
    case $(jq -r --arg r "$realm" '.[$r]' <<<"$stopgap") in
    enabled)
      [[ $(jq -r .eventsEnabled <<<"$rep") == false ]] && pass "realm $realm: event store off again" ||
        fail "realm $realm: event store still on" ;;
    added-login)
      jq -e '.enabledEventTypes | index("LOGIN") | not' <<<"$rep" >/dev/null && pass "realm $realm: LOGIN removed again" ||
        fail "realm $realm: LOGIN still stored" ;;
    esac
  done
  [[ -z $(keycloak GET "/admin/realms/$ORG/clients?clientId=contrib-eventing-login-test" | jq -r '.[0].id // empty') ]] &&
    pass "login test client gone" || fail "login test client still there"

  log "Install works after uninstall"
  install
  pass "install.sh"
fi

log "A second install changes nothing"
before=$(state)
install
# Give controllers time to act on anything the install changed.
sleep 20
after=$(state)
if [[ $before == "$after" ]]; then
  pass "nothing changed"
else
  fail "changed:"
  diff <(echo "$before") <(echo "$after") | head -40
fi

if ((failed)); then
  echo "install check failed"
  exit 1
fi
echo "install check passed"
