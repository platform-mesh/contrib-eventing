# Gets kcp credentials for the subscription controller and the OpenFGA adapter.
# We ask the platform-mesh-operator for provider connections: for each it
# creates a ServiceAccount with exactly the rights the APIExport needs and
# writes a kubeconfig for it into our namespace.
#
# The export must exist before this runs. The operator handles all provider
# connections in one loop and stops at the first one that fails.

log "Requesting a kcp kubeconfig from the platform-mesh-operator"
kind_kubectl create namespace "$NS" --dry-run=client -o yaml | kind_kubectl apply -f - >/dev/null

# The second connection lets the OpenFGA adapter read AccountInfo through the
# core.platform-mesh.io export, to put account paths into events. iam-service
# gets the same. The operator grants all verbs on the export's resources and
# has no read-only option, though the adapter only reads.
connections=$(jq -nc --arg ns "$NS" '[{
  path: "root:providers:eventing",
  apiExportNames: ["eventing.platform-mesh.io"],
  secret: "contrib-eventing-kcp",
  namespace: $ns,
  adminAuth: false
}, {
  path: "root:platform-mesh-system",
  apiExportNames: ["core.platform-mesh.io"],
  secret: "contrib-eventing-core",
  namespace: $ns,
  adminAuth: false
}]')
existing=$(kind_kubectl -n platform-mesh-system get platformmesh platform-mesh -o json |
  jq -c '.spec.kcp.extraProviderConnections // []')
missing=$(jq -c --argjson have "$existing" '[.[] | select(.secret as $s | $have | any(.secret == $s) | not)]' <<<"$connections")
if [[ $missing != "[]" ]]; then
  patch=$(jq -nc --argjson list "$existing" --argjson add "$missing" \
    '{spec: {kcp: {extraProviderConnections: ($list + $add)}}}')
  kind_kubectl -n platform-mesh-system patch platformmesh platform-mesh --type merge -p "$patch" >/dev/null
fi
for secret in contrib-eventing-kcp contrib-eventing-core; do
  wait_for "Secret $NS/$secret" 180 kind_kubectl -n "$NS" get secret "$secret"
done
