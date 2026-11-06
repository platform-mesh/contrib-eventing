# Milestone 2

Goal: a first consumer can use eventing
([backlog#323](https://github.com/platform-mesh/backlog/issues/323)).

## What the first consumer needs

Their own fork of iam-service publishes the full member list of an account,
with names, after every membership change and after every login. That is on
purpose: a missed event doesn't matter, because the next one carries the whole
list. Their subscribers resync through an API when they need to. They can't
say whether names or ordering are used, because they don't know all their
subscribers.

Our events are single changes, so we add full member lists as a second kind of
event and let each subscriber choose.

## Work

1. **Members snapshot event.** Subscribers that keep member lists need the
   whole list, not single changes: then a missed event is healed by the next
   one, and order doesn't matter, they keep the newest. A new type
   `io.platform-mesh.iam.account.members.v1alpha1` next to `granted` and
   `revoked`; a Subscription picks what it wants through `filter.types`, so the
   API stays as it is. For each page of the OpenFGA changelog the adapter
   collects the accounts that changed, reads their current members from
   OpenFGA and sends one snapshot per account. No names for now; they are not
   in OpenFGA (see milestone 3). Draft in the [contract](contract.md).
2. **Account path in events.** A consumer that wants more than the event, for
   example the members with names, asks iam-service. Its GraphQL API addresses
   an account by its kcp path (`ResourceContext.accountPath`, e.g.
   `root:orgs:acme:team-a`), and our events only have the cluster id and the
   name. Add `accountPath` to membership and snapshot events. OpenFGA only gives
   the adapter the parent's cluster id; the path is in the parent workspace's
   `AccountInfo` (`spec.account.path`), plus the account name. Open: how we get
   read access to `AccountInfo` in all account workspaces, to be agreed with
   the Platform Mesh owners. Paths can be cached by cluster id.
3. **Block replies.** Every consumer trusts that events come from the
   platform. Today any subscriber can break that: if its endpoint answers with
   a CloudEvent, Knative puts that event into the broker past our ingress, and
   all other subscribers get it. Tested locally with a forged grant for another
   org. Fix: drop the `knative-eventing` rule from the broker NetworkPolicy
   (tested, e2e passes).
4. **Dead letters and timeout.** A sink that is down long enough loses events
   without a trace: with the default of 10 retries Knative waits about 3.5
   minutes in total and then drops them. A consumer needs a place to find what
   it missed. A timeout keeps one slow request from holding a delivery open.
   Add `deadLetterSink` and `timeout` to `Subscription.spec.delivery`.
5. **Sink allowlist.** Events carry user ids, usually email addresses, and
   today a Subscription can send them to any URL, including services inside the
   cluster. The operator should decide where that data may go. The operator
   sets allowed hosts; other sinks get `Ready=False`, reason `SinkNotAllowed`.
6. **Try the NATS JetStream channel.** The in-memory channel accepts events
   before delivering them, so a restart of its dispatcher loses undelivered
   ones, and the adapter has already moved on. The JetStream channel stores an
   event before it answers and gives every subscription its own durable
   consumer, so a restart should lose nothing. Only the channel behind our
   broker changes. Try it locally: e2e, a failing sink, a dispatcher restart.

Order: 1 and 2 first, they shape the contract. 3 to 5 alongside, they don't
depend on each other. 6 after that.

## Decided

- Delivery stays HTTP via Knative. Consumers that use a message bus bridge to
  it themselves.
- Both single changes and full member lists, chosen by event type.
- Replies: NetworkPolicy now, Knative EventPolicies later.
- Subscriptions for platform admins now, tenant-scoped later.
- Not in this milestone: provider publishing, Keycloak events, names in
  snapshots, chart and `ManagedProvider` install, the RFC.

## Considerations

**Message bus support.** We could ship a forwarder to Pub/Sub or Kafka, but
that is one more component per bus, and it only helps the consumers who use
that bus. An HTTP endpoint that writes to their own topic is small, and they
control it. Knative's own GCP integration is archived, so there is nothing to
build on there either.

**Snapshots: read or cache.** Reading the members from OpenFGA for each
changed account is simple and always right. Membership changes are rare,
mostly people clicking in the portal; bulk changes are covered by sending one
snapshot per account per changelog page. A cache in memory would save the
reads, but it starts empty: the adapter begins with existing orgs at "now",
and a restart clears it, so the first snapshots would miss earlier members.
It would need a full load at start or a read on the first change per account.
We add it if OpenFGA suffers from the reads.

**Asking iam-service instead.** The first consumer offered to fetch the list
themselves if the event tells them which account changed. That needs a way in:
the members are only in OpenFGA, not in kcp, so a kcp admin kubeconfig doesn't
help, and the iam-service API wants a Keycloak token and checks
`get_iam_users` for the token's user on each account. There is no service
identity for that today. Snapshots work without it, so we send them, and the
path (item 2) keeps the API option open for anyone who has access.

**Snapshots: names.** The first consumer's lists have names, and they can't
rule out that a subscriber uses them. Names are in Keycloak, not OpenFGA, and
reading them needs Keycloak access, which comes with milestone 3. Until then
subscribers that need names look them up themselves.

**NetworkPolicy or EventPolicies for replies.** The NetworkPolicy is a small
change and tested. The subscriber gets the event once; the blocked reply is
retried with the Subscription's retries, and then the original event goes to
the dead letter sink if one is set (see the [architecture](architecture.md)
known gaps). The proper
fix is Knative OIDC with an EventPolicy per broker, which also checks every
sender and would let other teams use Knative in the same cluster. But it is
alpha, switched on for the whole cluster, needs our ingress to send tokens, and
is untested. Revisit it when it is beta.

**Durable delivery.** A message bus on the consumer side doesn't help with the
restart loss, because it happens before their bridge. Snapshots make the gap
smaller but don't close it: the last change before a restart may never arrive.
Options with a release for Knative v1.23:

| Option | Maturity | Needs | Effect on us |
| --- | --- | --- | --- |
| NATS JetStream channel | beta | a NATS server | only the channel changes |
| Kafka broker | GA | a Kafka cluster | own ingress, item 3 has to be redone |
| RabbitMQ broker | GA | RabbitMQ and its operators | own ingress, item 3 has to be redone |

JetStream is the smallest step, so we try it first. The JetStream channel
answers only after `js.Publish` succeeded, which is what we need; we haven't
run it yet. It may also make the async-handler setting of the in-memory channel
unnecessary, since every subscription has its own consumer.

If JetStream doesn't work out, the fallback is resending from OpenFGA's
changelog, which is never pruned: the subscription controller, which knows
Knative anyway, notices a dispatcher restart and writes a resend point into the
adapter's state, and the adapter reads the changelog again from there.
Duplicates are fine, the event `id` stays the same. Turning background delivery
off again would also avoid the loss, but one failing sink would slow down all
of them again.

**Account deletion.** Deleting an account removes its role tuples, and one
`revoked` per member arrives (tested with security-operator v0.36.0). The
snapshot after that will be an empty list. Deleting a whole org removes its store and
sends nothing.

## Outlook: milestone 3

Keycloak events: login, user created, user deleted. The first consumer gets
these from their fork today, too. Keycloak access also brings names for the
snapshots.

A prototype for login is on the branch `feat/keycloak-login`. It polls the
Keycloak event store of each realm and switches the store on for `LOGIN`
events with a retention time. It logs in as a service account with the admin
role.

To settle before building it:

- Switching on the event store changes realms that security-operator manages.
  That needs the security-operator owners' agreement.
- The admin role is more than the adapter needs; it should only read events
  and users.
- User created and deleted are admin events in Keycloak, a separate store the
  prototype doesn't read yet.
- Events older than the retention time are gone, so a long adapter outage
  leaves a gap.
- Events identify users the same way membership events do (`user.id`).
