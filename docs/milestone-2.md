# Milestone 2

Goal: a first consumer can use eventing
([backlog#323](https://github.com/platform-mesh/backlog/issues/323)).

## What the first consumer has today

Their own fork of iam-service publishes the full member list of an account,
with names, after every membership change and after every login. Our events
are single changes.

## Work

1. **Block replies.** Every consumer trusts that events come from the
   platform. Today any subscriber can break that: if its endpoint answers with
   a CloudEvent, Knative puts that event into the broker past our ingress, and
   all other subscribers get it. Tested locally with a forged grant for another
   org. Fix: drop the `knative-eventing` rule from the broker NetworkPolicy
   (tested, e2e passes).
2. **Dead letters and timeout.** A sink that is down long enough loses events
   without a trace: with the default of 10 retries Knative waits about 3.5
   minutes in total and then drops them. A consumer needs a place to find what it missed. A timeout keeps one
   slow request from holding a delivery open, and it puts an upper bound on
   how long a delivery can take, which item 4 needs. Add `deadLetterSink` and
   `timeout` to `Subscription.spec.delivery`.
3. **Sink allowlist.** Events carry user ids, usually email addresses, and
   today a Subscription can send them to any URL, including services inside the
   cluster. The operator should decide where that data may go. The operator
   sets allowed hosts; other sinks get `Ready=False`, reason `SinkNotAllowed`.
4. **Resend after a dispatcher restart.** The in-memory channel accepts events
   before delivering them, so a restart loses undelivered ones, and the adapter
   has already moved on. The consumer would have a gap and not know it. The
   controller notices the restart and writes a resend point into the adapter's
   state; the adapter reads the OpenFGA changelog again from there. Duplicates
   are fine, the event `id` stays the same. First reproduce the loss locally.
5. **Contract check with the first consumer.** The contract is a draft, and
   changing it after a consumer has built on it breaks them. Their model today
   differs from ours (member lists, names), so we walk through it before they
   start: changes instead of member lists, which fields they need.

1 to 3 don't depend on each other. 4 needs 2. 5 can run alongside.

## Decided

- Delivery stays HTTP via Knative. Consumers that use a message bus bridge to
  it themselves.
- Replies: NetworkPolicy now, Knative EventPolicies later.
- Subscriptions for platform admins now, tenant-scoped later.
- No members snapshot event for now.
- No durable broker for now.
- Not in this milestone: provider publishing, Keycloak events, chart and
  `ManagedProvider` install, the RFC.

## Considerations

**Message bus support.** We could ship a forwarder to Pub/Sub or Kafka, but
that is one more component per bus, and it only helps the consumers who use
that bus. An HTTP endpoint that writes to their own topic is small, and they
control it. Knative's own GCP integration is archived, so there is nothing to
build on there either.

**NetworkPolicy or EventPolicies for replies.** The NetworkPolicy is a small
change and tested. A blocked reply is retried for about 90 seconds and then
dropped, which is harmless because delivery runs in the background. The proper
fix is Knative OIDC with an EventPolicy per broker, which also checks every
sender and would let other teams use Knative in the same cluster. But it is
alpha, switched on for the whole cluster, needs our ingress to send tokens, and
is untested. Revisit it when it is beta.

**Snapshots.** The first consumer keeps member lists today. From single changes
they can build that list, but they cannot resync after a gap, and older changes
are not replayed. A snapshot event would need the adapter to read all role
tuples of an account after each change, and names would need a Keycloak
lookup. We leave it out until the consumer says they need it.

**Durable broker.** Without one, the in-memory channel loses events it has
accepted but not yet delivered when its dispatcher restarts, and the adapter has
already moved its position. A message bus on the consumer side doesn't help,
because the loss happens before their bridge. Instead of a broker, item 4
resends from the OpenFGA changelog, which is never pruned. Options for later,
each with a release for Knative v1.23 (none tried yet):

| Option | Maturity | Needs | Effect on us |
| --- | --- | --- | --- |
| Kafka broker | GA | a Kafka cluster | own ingress, item 1 has to be redone |
| RabbitMQ broker | GA | RabbitMQ and its operators | own ingress, item 1 has to be redone |
| NATS JetStream channel | beta | a NATS server | only the channel changes |

**Resend: who notices the restart.** The controller, not the adapter: the
adapter should not know about Knative, the controller does anyway. The other
way to avoid the loss is turning background delivery off again and keeping
every delivery under the ingress timeout. We dropped that: one failing sink
would slow down all of them again, and a longer timeout brings the duplicates
back.

## Outlook: milestone 3

Keycloak events: login, user created, user deleted. The first consumer gets
these from their fork today, too.

A prototype for login is on the branch `feat/keycloak-login`. It polls the
Keycloak event store of each realm and switches the store on for `LOGIN`
events with a retention time. It logs in as a service account with the admin
role.

To settle before building it:

- Switching on the event store changes realms that security-operator manages.
  That needs the security-operator owners' agreement.
- The admin role is more than the adapter needs; it should only read events.
- User created and deleted are admin events in Keycloak, a separate store the
  prototype doesn't read yet.
- Events older than the retention time are gone, so a long adapter outage
  leaves a gap. Same question as item 4 for OpenFGA.
- Events identify users the same way membership events do (`user.id`).
