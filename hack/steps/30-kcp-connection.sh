# Gets kcp credentials for the OpenFGA adapter to read AccountInfo through the
# core.platform-mesh.io export, to put account paths into events. iam-service
# gets the same. The platform-mesh-operator creates a ServiceAccount with the
# export's rights and writes a kubeconfig for it next to the ManagedProvider.
# The adapter only reads, so the connection is read-only (get, list, watch)
# where the operator supports it (platform-mesh#591). Older operators don't
# know readOnly and grant all verbs on the export's resources.

log "Requesting a kcp kubeconfig for core.platform-mesh.io"
read_only=false
if kind_kubectl get crd platformmeshes.core.platform-mesh.io -o json |
  jq -e 'any(.spec.versions[].schema.openAPIV3Schema.properties.spec.properties.kcp.properties.extraProviderConnections.items.properties; has("readOnly"))' >/dev/null; then
  read_only=true
else
  log "The platform-mesh-operator has no readOnly connections yet; the core connection gets all verbs"
fi
existing=$(kind_kubectl -n platform-mesh-system get platformmesh platform-mesh -o json |
  jq -c '.spec.kcp.extraProviderConnections // []')
index=$(jq '(map(.secret) | index("contrib-eventing-core")) // empty' <<<"$existing")
if [[ -z $index ]]; then
  patch=$(jq -nc --argjson list "$existing" --arg ns "$NS" --argjson ro "$read_only" '{spec: {kcp: {extraProviderConnections: ($list + [{
    path: "root:platform-mesh-system",
    apiExportNames: ["core.platform-mesh.io"],
    secret: "contrib-eventing-core",
    namespace: $ns,
    adminAuth: false
  } + if $ro then {readOnly: true} else {} end])}}}')
  kind_kubectl -n platform-mesh-system patch platformmesh platform-mesh --type merge -p "$patch" >/dev/null
elif $read_only && [[ $(jq -r ".[$index].readOnly // false" <<<"$existing") != true ]]; then
  # Installed before the operator could do it.
  kind_kubectl -n platform-mesh-system patch platformmesh platform-mesh --type json     -p "[{\"op\": \"add\", \"path\": \"/spec/kcp/extraProviderConnections/$index/readOnly\", \"value\": true}]" >/dev/null
fi
wait_for "Secret $NS/contrib-eventing-core" 180 kind_kubectl -n "$NS" get secret contrib-eventing-core
