# Deploys the subscription controller, the OpenFGA adapter and the ingress.

# Sinks may only point at these hosts: the demo sinks, comma separated, exact
# or *.suffix.
ALLOWED_SINK_HOSTS=${ALLOWED_SINK_HOSTS:-*.eventing-demo-sink.svc.cluster.local}

log "Deploying $IMAGE_TAGGED"
TRAEFIK_IP=$(exposure traefikClusterIP)
sed -e "s|\${IMAGE}|$IMAGE_TAGGED|g" -e "s|\${BASE_DOMAIN}|$BASE_DOMAIN|g" -e "s|\${TRAEFIK_IP}|$TRAEFIK_IP|g" \
  -e "s|\${ALLOWED_SINK_HOSTS}|$ALLOWED_SINK_HOSTS|g" \
  "$ROOT/config/deploy/eventing.yaml" | kind_kubectl apply -f - >/dev/null
kind_kubectl -n "$NS" rollout status deploy/subscription-controller deploy/openfga-adapter deploy/ingress --timeout=120s >/dev/null
