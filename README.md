# Platform Mesh Eventing

Eventing lets other systems react to what happens in Platform Mesh without polling it.

Today, when someone is added to an account, a user logs in, or a provider changes a resource, nothing tells anyone else. Every system that cares has to poll OpenFGA, Keycloak or kcp itself, map the data on its own, and handle retries. This repository adds an optional module that turns those changes into [CloudEvents](https://cloudevents.io) and delivers them to subscribers.

## How it works

```mermaid
flowchart LR
  fga[(OpenFGA)] -- changelog --> adapter[OpenFGA adapter]
  adapter -- event --> ingress[Ingress]
  ingress -- checked sender --> delivery["Delivery<br/>(Knative, NATS JetStream)"]
  delivery -- event --> sink[Your endpoint]
  subs[Subscription in kcp] -. what goes where .-> delivery
```

OpenFGA can't send events, so the adapter reads its changelog and turns a change like "Alice became member of team-a" into an event. A platform admin creates a Subscription in the eventing workspace in kcp (which events, which URL), and delivery sends matching events there, with retries. There is one stream for the whole platform; every event names the org it belongs to.

Later more systems get adapters (Keycloak, kcp), and providers will be able to send their own events too.

More detail: [architecture](docs/architecture.md), [event contract](docs/contract.md), [receiving events](docs/subscribing.md), [decisions](docs/adr/).

## What it does not do

- It does not change any existing Platform Mesh component. Eventing observes the system from outside and can be installed or removed independently.
- It does not connect to message brokers on the subscriber side. A subscriber that needs GCP Pub/Sub, Kafka or similar runs its own bridge behind an HTTP endpoint.

## What it covers today

- Membership events (a user gets or loses a role on an account) and a snapshot
  of an account's members after each change, read from OpenFGA. Events carry the
  account's workspace path.
- Subscriptions for platform admins, covering all orgs or some of them, with
  retries, timeouts and dead letter sinks. Sinks must be on the platform
  operator's allowlist.
- Events are stored in NATS JetStream before delivery, so restarts lose nothing.

What comes after is in the [roadmap](docs/roadmap.md).

## Try it locally

You need a local Platform Mesh on kind, installed as described in
[helm-charts/installation](https://github.com/platform-mesh/helm-charts/tree/main/installation),
and `kubectl`, `docker`, `curl` and `jq`. The install adds Knative Eventing and a
single NATS JetStream server; Knative in that cluster must be used by eventing only (see [architecture](docs/architecture.md#how-it-plugs-in)).

```sh
./hack/install.sh     # adds eventing, a subscribed sink and a demo org eventing-demo
./hack/add-member.sh  # gives a user the member role on the demo org
kubectl -n eventing-demo-sink logs deploy/event-display
```

The sink shows a `membership.granted` event. `./hack/add-member.sh --remove`
takes the role away again, `./test/e2e.sh` checks the whole path,
`./test/install-check.sh --uninstall` checks that installing again changes
nothing and uninstalling leaves nothing, and `./hack/uninstall.sh` removes
everything.

## Background

- Epic: [platform-mesh/backlog#323](https://github.com/platform-mesh/backlog/issues/323)
- Spike and results: [platform-mesh/backlog#421](https://github.com/platform-mesh/backlog/issues/421)
- Modularization: [RFC 008](https://github.com/platform-mesh/architecture/blob/main/rfc/008-platform-mesh-modularization.md)
