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
