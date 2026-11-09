# Shared settings and helpers for the hack/ scripts. Source it, don't run it.
#
# Two clusters are involved and every command says which one it talks to:
#   kind   the kind cluster that runs Platform Mesh (kubectl context)
#   kcp    the kcp control plane inside it, reached through the Traefik port
#          that the kind install maps to the host

set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SECRET_DIR=$ROOT/.secret

KUBE_CONTEXT=${KUBE_CONTEXT:-kind-platform-mesh}
KIND_NODE=${KIND_NODE:-platform-mesh-control-plane}
# The kind install maps Traefik to this host address (installation/README.md).
INGRESS_ADDRESS=${INGRESS_ADDRESS:-127.0.0.1}
ORG=${ORG:-eventing-demo}
DEMO_USER=${DEMO_USER:-$ORG@example.com}
KNATIVE_VERSION=${KNATIVE_VERSION:-v1.23.1}
NATSS_VERSION=${NATSS_VERSION:-v1.23.3}
IMAGE=${IMAGE:-ghcr.io/platform-mesh/contrib-eventing:dev}
NS=contrib-eventing

log() { printf '\033[1m==> %s\033[0m\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

kind_kubectl() { kubectl --context "$KUBE_CONTEXT" "$@"; }

# The domain and port come from the PlatformMesh resource, so the scripts work
# with whatever base domain the installation was done with.
exposure() {
  kind_kubectl -n platform-mesh-system get platformmesh platform-mesh \
    -o jsonpath="{.spec.exposure.$1}"
}
BASE_DOMAIN=${BASE_DOMAIN:-$(exposure baseDomain)}
PORT=${PORT:-$(exposure port)}
KCP_HOST=kcp.api.$BASE_DOMAIN

# kcp <workspace path> <kubectl args...>
# No /etc/hosts entries needed: we connect to the ingress address and send the
# kcp host name for TLS, so the certificate is still checked.
kcp() {
  local ws=$1
  shift
  kubectl --kubeconfig "$SECRET_DIR/kcp-admin.kubeconfig" \
    --server "https://$INGRESS_ADDRESS:$PORT/clusters/$ws" \
    --tls-server-name "$KCP_HOST" "$@"
}

# keycloak <method> <path below /keycloak> [curl args...]
# Calls Keycloak through the ingress as the Keycloak admin. Install scripts
# only: the components never get admin. Admin tokens of the master realm live 60
# seconds, so we log in again when ours is older than 30. The credentials stay
# in local variables of keycloak_login and are never printed.
keycloak() {
  local method=$1 path=$2
  shift 2
  if [[ -z ${KEYCLOAK_TOKEN:-} ]] || ((SECONDS - ${KEYCLOAK_TOKEN_AT:-0} > 30)); then
    keycloak_login
  fi
  curl -sS --fail-with-body --cacert "$SECRET_DIR/domain-ca.crt" \
    --resolve "$BASE_DOMAIN:$PORT:$INGRESS_ADDRESS" \
    -X "$method" -H "Authorization: Bearer $KEYCLOAK_TOKEN" \
    -H 'Content-Type: application/json' \
    "https://$BASE_DOMAIN:$PORT/keycloak$path" "$@"
}

# Logs in as the Keycloak admin from the keycloak-admin Secret.
keycloak_login() {
  local user pass
  user=$(kind_kubectl -n platform-mesh-system get secret keycloak-admin -o jsonpath='{.data.username}' | base64 -d)
  pass=$(kind_kubectl -n platform-mesh-system get secret keycloak-admin -o jsonpath='{.data.password}' | base64 -d)
  KEYCLOAK_TOKEN=$(curl -sS --fail-with-body --cacert "$SECRET_DIR/domain-ca.crt" \
    --resolve "$BASE_DOMAIN:$PORT:$INGRESS_ADDRESS" \
    "https://$BASE_DOMAIN:$PORT/keycloak/realms/master/protocol/openid-connect/token" \
    -d grant_type=password -d client_id=admin-cli \
    --data-urlencode "username=$user" --data-urlencode "password=$pass" | jq -r .access_token)
  KEYCLOAK_TOKEN_AT=$SECONDS
}

# wait_for <description> <timeout seconds> <command...>
wait_for() {
  local what=$1 timeout=$2
  shift 2
  local start=$SECONDS
  until "$@" >/dev/null 2>&1; do
    ((SECONDS - start < timeout)) || die "timed out waiting for $what"
    sleep 3
  done
}
