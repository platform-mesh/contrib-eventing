# Installs Knative Eventing with the channel based broker, and NATS JetStream
# for the broker's channel: it stores an event before the broker accepts it, so
# restarts lose nothing.

# Eventing locks down Knative's broker ingress, which every Broker in the
# cluster shares. Brokers of anyone else would stop receiving events.
if kind_kubectl get crd brokers.eventing.knative.dev >/dev/null 2>&1; then
  others=$(kind_kubectl get brokers.eventing.knative.dev -A --no-headers 2>/dev/null | awk -v ns="$NS" '$1 != ns {print $1 "/" $2}')
  [[ -z $others ]] || die "Knative in this cluster has other Brokers ($others); eventing needs Knative for itself"
fi

log "Installing NATS JetStream"
kind_kubectl apply -f "$ROOT/config/nats/nats.yaml" >/dev/null
kind_kubectl -n nats-io rollout status statefulset/nats --timeout=300s >/dev/null

log "Installing Knative Eventing $KNATIVE_VERSION and its JetStream channel $NATSS_VERSION"
dir=$SECRET_DIR/knative-$KNATIVE_VERSION
mkdir -p "$dir"
files=(eventing-crds.yaml eventing-core.yaml mt-channel-broker.yaml)
for f in "${files[@]}"; do
  [[ -f $dir/$f ]] || curl -sSfL -o "$dir/$f" \
    "https://github.com/knative/eventing/releases/download/knative-$KNATIVE_VERSION/$f"
done
[[ -f $dir/eventing-jsm.yaml ]] || curl -sSfL -o "$dir/eventing-jsm.yaml" \
  "https://github.com/knative-extensions/eventing-natss/releases/download/knative-$NATSS_VERSION/eventing-jsm.yaml"
if [[ $KNATIVE_VERSION == v1.23.1 && $NATSS_VERSION == v1.23.3 ]]; then
  (cd "$dir" && sha256sum --quiet -c "$ROOT/config/knative/sha256sums") || die "Knative checksum mismatch"
fi

kind_kubectl apply -f "$dir/eventing-crds.yaml" >/dev/null
kind_kubectl wait --for=condition=Established crd --all --timeout=60s >/dev/null
for f in "${files[@]:1}" eventing-jsm.yaml; do
  kind_kubectl apply -f "$dir/$f" >/dev/null
done
kind_kubectl apply -f "$ROOT/config/nats/config-nats.yaml" -f "$ROOT/config/nats/config-br-default-channel.yaml" >/dev/null
kind_kubectl -n knative-eventing wait --for=condition=Available deploy --all --timeout=300s >/dev/null
