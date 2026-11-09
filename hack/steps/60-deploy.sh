# Deploys the subscription controller, the OpenFGA and Keycloak adapters and the ingress.

# Sinks may only point at these hosts: the demo sinks, comma separated, exact
# or *.suffix.
ALLOWED_SINK_HOSTS=${ALLOWED_SINK_HOSTS:-*.eventing-demo-sink.svc.cluster.local}

# Both adapters use the same user claim as security-operator and iam-service, so
# user ids in events are the ones the rest of the platform uses. Only email and
# sub are supported; the adapters refuse to start with anything else.
USER_CLAIM=$(kind_kubectl -n platform-mesh-system get deploy security-operator -o json |
  jq -r '[.spec.template.spec.containers[] | select(.name == "security-operator") | (.args // [])[] |
    select(startswith("--user-claim=")) | ltrimstr("--user-claim=")] | first // "email"')

log "Deploying $IMAGE_TAGGED (user claim $USER_CLAIM)"
TRAEFIK_IP=$(exposure traefikClusterIP)
sed -e "s|\${IMAGE}|$IMAGE_TAGGED|g" -e "s|\${BASE_DOMAIN}|$BASE_DOMAIN|g" -e "s|\${TRAEFIK_IP}|$TRAEFIK_IP|g" \
  -e "s|\${ALLOWED_SINK_HOSTS}|$ALLOWED_SINK_HOSTS|g" -e "s|\${USER_CLAIM}|$USER_CLAIM|g" \
  "$ROOT/config/deploy/eventing.yaml" | kind_kubectl apply -f - >/dev/null
kind_kubectl -n "$NS" rollout status deploy/subscription-controller deploy/openfga-adapter deploy/keycloak-adapter deploy/ingress --timeout=120s >/dev/null
