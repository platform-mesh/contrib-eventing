# Receiving events

This is for platform admins who want the membership events of the platform
delivered to their own HTTP endpoint.

## 1. Provide an endpoint

Your endpoint receives one CloudEvent per `POST`, in CloudEvents HTTP binary
mode: the attributes come as `ce-*` headers (`ce-id`, `ce-type`, `ce-source`,
`ce-subject`, `ce-time`, `ce-pmorg`), the data as the JSON body.
[api/openapi/subscriber.yaml](../api/openapi/subscriber.yaml) describes the
requests and the answers your endpoint must give; [contract.md](contract.md)
explains the fields.

The rules that matter:

- Answer with a 2xx and an **empty body**, quickly. A body that is not a
  CloudEvent counts as a failed delivery. A body that is a CloudEvent is
  published again as a new event, so never send one.
- 404, 408, 409, 429 and 5xx are retried with backoff; any other status is not.
- The same event can arrive more than once. Drop events whose `ce-id` you have
  already seen. There is no order between events.
- The endpoint must be reachable from inside the cluster, for example as a
  Kubernetes Service. Requests come without credentials.

## 2. Create a Subscription

Subscriptions live in the workspace `root:providers:eventing`, which only
platform admins can reach. With the repo's scripts on a local setup:

```sh
source hack/lib.sh
kcp root:providers:eventing apply -f - <<EOF
apiVersion: eventing.platform-mesh.io/v1alpha1
kind: Subscription
metadata:
  name: my-consumer
spec:
  filter:
    types:
      - io.platform-mesh.iam.membership.granted.v1alpha1
      - io.platform-mesh.iam.membership.revoked.v1alpha1
    orgs: [acme]   # optional; without it you get all orgs
  sink:
    uri: http://my-consumer.my-namespace.svc.cluster.local
  delivery:
    retry: 3
EOF
kcp root:providers:eventing get subscriptions
```

The Subscription is ready when `READY` is `True`; otherwise `REASON` says what
is missing. Events start arriving with the next membership change. Deleting the
Subscription stops delivery.

## Limits in milestone 1

- Replies are not blocked yet: an endpoint that answers with a CloudEvent
  injects that event into the platform stream, past the ingress. This is fixed
  before a first consumer (milestone 2).
- Any sink URL is accepted, and events not yet delivered are lost when the
  in-memory channel restarts.
