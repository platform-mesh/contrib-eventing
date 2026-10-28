# Deploys the subscription controller, the OpenFGA adapter and the ingress.

# The broker channel delivers in the background (see internal/delivery/knative).
# The controller sets that on the Broker and Knative copies it to the channel,
# but the dispatcher reads it only when it first sees a channel. A Broker from
# an older install is switched here, without waiting for the controller, and
# the dispatcher restarted once the channel has the setting.
async() { [[ $(kind_kubectl -n "$NS" get "$1" -o jsonpath='{.metadata.annotations.messaging\.knative\.dev/async-handler}' 2>/dev/null) == true ]]; }
upgrade=false
if kind_kubectl -n "$NS" get broker.eventing.knative.dev default >/dev/null 2>&1 && ! async broker.eventing.knative.dev/default; then
  upgrade=true
fi

log "Deploying $IMAGE_TAGGED"
TRAEFIK_IP=$(exposure traefikClusterIP)
# Users are identified the same way security-operator identifies them.
USER_CLAIM=$(kind_kubectl -n platform-mesh-system get deploy security-operator -o json |
  jq -r '[.spec.template.spec.containers[0].args[] | select(startswith("--user-claim=")) | ltrimstr("--user-claim=")][0] // "email"')
sed -e "s|\${IMAGE}|$IMAGE_TAGGED|g" -e "s|\${BASE_DOMAIN}|$BASE_DOMAIN|g" -e "s|\${TRAEFIK_IP}|$TRAEFIK_IP|g" \
  -e "s|\${USER_CLAIM}|$USER_CLAIM|g" "$ROOT/config/deploy/eventing.yaml" | kind_kubectl apply -f - >/dev/null
kind_kubectl -n "$NS" rollout status deploy/subscription-controller deploy/openfga-adapter deploy/ingress --timeout=120s >/dev/null

if $upgrade; then
  log "Switching the broker channel to background delivery"
  kind_kubectl -n "$NS" annotate broker.eventing.knative.dev default messaging.knative.dev/async-handler=true --overwrite >/dev/null
  wait_for "the async setting on the broker channel" 120 async inmemorychannel/default-kne-trigger
  kind_kubectl -n knative-eventing rollout restart deploy/imc-dispatcher >/dev/null
  kind_kubectl -n knative-eventing rollout status deploy/imc-dispatcher --timeout=120s >/dev/null
fi
