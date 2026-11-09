# STOPGAP until platform-mesh/platform-mesh#578.
#
# The Keycloak adapter and the OpenFGA adapter read login events and user names
# from Keycloak with a client of the master realm that has only the roles
# view-events and view-users on the org realms, never admin. Platform Mesh will
# create that client and switch on the event store itself (#578). Until then
# this step does the same, as the Keycloak admin:
#   - the master client contrib-eventing-keycloak-reader with a service account
#   - view-events and view-users on the management client of every org realm
#   - the event store of every org realm, storing LOGIN only
#
# Orgs created after this ran are not covered until #578 lands; running
# ./hack/install.sh again covers them. What was changed is recorded in the
# ConfigMap keycloak-reader-stopgap, so uninstall.sh can undo exactly that.

KEYCLOAK_READER=contrib-eventing-keycloak-reader
# How long Keycloak keeps stored events, in seconds. Logins older than this are
# lost if the adapter is down that long.
KEYCLOAK_EVENTS_RETENTION=${KEYCLOAK_EVENTS_RETENTION:-604800}

log "Setting up the Keycloak reader client (stopgap until platform-mesh#578)"

reader_uuid=$(keycloak GET "/admin/realms/master/clients?clientId=$KEYCLOAK_READER" | jq -r '.[0].id // empty')
if [[ -z $reader_uuid ]]; then
  # No realm roles are assigned. The service account only gets what is mapped below.
  keycloak POST /admin/realms/master/clients -o /dev/null -d "$(jq -nc --arg id "$KEYCLOAK_READER" '{
    clientId: $id, name: "contrib-eventing reader (read-only stopgap, see platform-mesh#578)",
    publicClient: false, serviceAccountsEnabled: true, standardFlowEnabled: false,
    directAccessGrantsEnabled: false, clientAuthenticatorType: "client-secret"}')"
  reader_uuid=$(keycloak GET "/admin/realms/master/clients?clientId=$KEYCLOAK_READER" | jq -r '.[0].id')
fi
reader_sa=$(keycloak GET "/admin/realms/master/clients/$reader_uuid/service-account-user" | jq -r .id)

kind_kubectl -n "$NS" create configmap keycloak-reader-stopgap --dry-run=client -o yaml |
  kind_kubectl -n "$NS" create -f - >/dev/null 2>&1 || true

# record <realm> <enabled|added-login>: adds the key, never rewrites one.
record() {
  if [[ -z $(kind_kubectl -n "$NS" get configmap keycloak-reader-stopgap -o "jsonpath={.data['$1']}") ]]; then
    kind_kubectl -n "$NS" patch configmap keycloak-reader-stopgap --type merge \
      -p "$(jq -nc --arg r "$1" --arg v "$2" '{data: {($r): $v}}')" >/dev/null
  fi
}

for realm in $(keycloak GET /admin/realms | jq -r '.[].realm'); do
  [[ $realm == master || $realm == welcome ]] && continue

  # Decide from the realm itself. While the store is off, events/config lists
  # every event type, but the realm stores an empty list, which Keycloak
  # treats as all types.
  rep=$(keycloak GET "/admin/realms/$realm")
  cfg=$(keycloak GET "/admin/realms/$realm/events/config")
  if [[ $(jq -r .eventsEnabled <<<"$rep") != true ]]; then
    new=$(jq -c --argjson exp "$KEYCLOAK_EVENTS_RETENTION" \
      '.eventsEnabled = true | .enabledEventTypes = ["LOGIN"] | .eventsExpiration = $exp' <<<"$cfg")
    keycloak PUT "/admin/realms/$realm/events/config" -o /dev/null -d "$new"
    record "$realm" enabled
  elif jq -e '(.enabledEventTypes // []) | length > 0 and (index("LOGIN") | not)' <<<"$rep" >/dev/null; then
    new=$(jq -c '.enabledEventTypes += ["LOGIN"]' <<<"$cfg")
    keycloak PUT "/admin/realms/$realm/events/config" -o /dev/null -d "$new"
    record "$realm" added-login
  fi

  mc=$(keycloak GET "/admin/realms/master/clients?clientId=$realm-realm" | jq -r '.[0].id // empty')
  [[ -n $mc ]] || die "no management client $realm-realm in the master realm"
  roles=$(jq -sc '.' < <(
    for role in view-events view-users; do keycloak GET "/admin/realms/master/clients/$mc/roles/$role"; done))
  keycloak POST "/admin/realms/master/users/$reader_sa/role-mappings/clients/$mc" -o /dev/null -d "$roles"
done

# The secret is read, never regenerated, so a second install changes nothing.
keycloak GET "/admin/realms/master/clients/$reader_uuid/client-secret" | jq -j .value |
  kind_kubectl -n "$NS" create secret generic "$KEYCLOAK_READER" \
    --from-literal=client-id="$KEYCLOAK_READER" --from-file=client-secret=/dev/stdin \
    --dry-run=client -o yaml | kind_kubectl apply -f - >/dev/null
