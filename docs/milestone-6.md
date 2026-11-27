# Milestone 6

Goal: providers publish their own events. Beyond that, a proposal for orgs,
accounts and providers to subscribe themselves, each getting only the events
it is allowed to see.

It builds on [milestone 4](milestone-4.md), the installable module, and on
[milestone 5](milestone-5.md), everything the first consumer's fork sends.
Milestone 6 runs in parallel with it; nothing here changes milestone 5.

Provider publishing ([#14](https://github.com/platform-mesh/contrib-eventing/issues/14))
moved here from milestone 4 and is part of the epic's definition of done
([backlog#323](https://github.com/platform-mesh/backlog/issues/323)): a
provider can publish its own event, provider documentation. Together with
milestone 5, the first consumer confirming the contract and the agreed RFC,
it closes the epic.

Four parts, in this order:

- **A. Providers publish their own events.** A provider sends events through
  the ingress, only for orgs that use it and only for types it declared.
  **In the epic's definition of done.**
- **B. Org isolation.** One broker as today. The controller sets every
  org filter, a delivery guard checks every event's org again before it
  leaves, the ingress signs the labels, the allowlist knows orgs. Nothing new
  can subscribe yet; this is the ground the next parts stand on.
- **C. Org- and account-level subscriptions.** Org and account admins create
  Subscriptions in their own workspace.
- **D. Providers receiving events.** A provider subscribes to events of the
  orgs that use it.

**B, C and D are not in the epic's definition of done.** They are a proposal,
decided at the checkpoint: build them now, later or not at all. Part A doesn't
depend on them.

A comes first: it brings the first senders that aren't the platform's own
adapters. B comes before C and D because they open the platform to
subscribers who are not platform admins.

## Org isolation

If org A and org B both use provider 1 and provider 2, nothing of A may reach
anyone acting for B: not B's admins, not B's accounts, not provider 1 or 2
acting for B. Every part of this milestone has to keep that.

What already holds:

- **Publishing.** Only the platform's own adapters may publish. The ingress
  authenticates every sender before an event reaches the broker.
- **Subscribing.** Only platform admins subscribe today, in eventing's
  provider workspace (named by its `ManagedProvider` since milestone 4); orgs
  can't bind the export. A Subscription
  without `filter.orgs` gets every org, which is meant for the platform
  operator and nobody else.
- **Inside the cluster.** NetworkPolicies keep everything but our ingress away
  from the broker, the channel and NATS.

What this milestone has to close:

1. **Providers sending.** Provider 1 may label an event only with orgs (and
   accounts) that bound its export, and only with types it declared; it can
   never send `io.platform-mesh.*` types. The ingress sets `source` itself.
   Part A.
2. **One broker for all orgs.** Today orgs are kept apart only by each
   Trigger's `pmorg` filter, so one wrong filter leaks. Part B adds a second,
   independent check at delivery, so a leak needs two mistakes. Isolation
   stays in our code, not in Kubernetes namespaces (decision 2).
3. **Who sets the org of a subscription.** Today the subscriber writes
   `filter.orgs`. For anyone but platform admins it must come from where the
   Subscription lives, not from its spec. Part C.
4. **Providers receiving.** Provider 1 gets events only for orgs that bound
   it, and never provider 2's events unless the org shares them. Part D.
5. **Sinks.** The allowlist is per platform, so an org could point at a host
   meant for another org. Part B.
6. **Payloads.** The platform checks the label (`pmorg`, `source`), not the
   content. A provider that puts org A's data into an event labelled org B is
   the provider's bug. The contract and the provider guide say what the
   platform guarantees: the org and the source, not the data.

Every way to publish and subscribe gets the same e2e: two orgs that bound the
same two providers, events from all four combinations, and a check that each
subscriber got exactly what it may see and nothing else.

## Throughput

Measured in a spike on one kind node (Docker Desktop, 8 CPUs), one replica of
everything, Knative Eventing v1.23.1 with the JetStream channel and one NATS
server. Details and the script to repeat it: `spike/README.md` on the branch
`spike/delivery-guard` (not pushed yet).

| Events/s | End to end p50 / p99, without guard | With guard | Guard CPU |
| --- | --- | --- | --- |
| 100 | 2.8 / 4.9 ms | 3.1 / 6.3 ms | 0.09 cores |
| 300 | 2.3 / 5.3 ms | 2.9 / 6.3 ms | 0.26 cores |
| 600 | 2.5 / 13 ms | 2.8 / 22 ms | 0.46 cores |
| 1000 | 3.4 / 28 ms | 6.8 / 136 ms | 0.68 cores |

- Up to 600 events/s every event arrives; the guard adds about 0.5 ms at p50.
- At 1000 events/s the node is full. Knative's JetStream dispatcher is the first
  part to max out (one pod), not the guard: 894 events/s got through without
  the guard, 794 with it.
- **The ingress caps eventing at about 5 events/s today.** It asks the
  Kubernetes API who the sender is (a TokenReview) for every event, through a
  client limited to 5 requests per second. Part A item 3 fixes this; with it,
  the numbers above hold.
- A Trigger's org list costs Knative time for every event: 5000 orgs took 1.2
  cores at 300 events/s. Hence the limit of 500 orgs per Trigger in part D.

This is enough for the first consumer and the expected load for now. The
contract and the install guide state the measured range, so nobody plans
beyond it unknowingly.

Not measured: several replicas of the guard and the ingress, a slow or
failing sink behind the guard, memory, rule changes under load, a cluster
with more than one node. Measure these before anyone needs more than about
500 events/s.

## Work

### A. Providers publish their own events

Moved here from milestone 4
([#14](https://github.com/platform-mesh/contrib-eventing/issues/14)).

1. **ADR 0002.** How a provider authenticates to the ingress, how it declares
   the types it publishes, and how the ingress limits it to orgs and accounts
   that bound its APIExport. Merged before the code. Decision 1.
2. **Declaration API.** A kcp resource in the provider's workspace, in the
   eventing export, that names the provider and the event types it sends.
   Its status says whether the provider may publish and why not.
3. **TokenReview cache in the ingress.** Today the ingress creates a
   TokenReview per event through a client limited to 5 requests/s, which caps
   eventing at about 5 events/s (found in the spike, also true on `main`).
   Successful reviews are cached for a minute. Providers bring the first real
   traffic, so this comes with them.
4. **Ingress.** Accepts a provider's event only for a declared type, and only
   for an org (and account, `pmaccount`) that bound the provider's export. It
   sets `source` from the provider, never from the event, and checks `pmorg`
   and `pmaccount` against the bindings it reads through the core export.
   Decision 7.
   `io.platform-mesh.*` stays reserved for the platform's own adapters.
5. **Sample provider.** Installed through its own `ManagedProvider`,
   publishes one event that reaches a Subscription. The e2e also shows that an
   undeclared type, an org without the binding and a foreign `source` are
   rejected.
6. **Provider guide** in `docs/`, and the contract says which parts of a
   provider event the platform guarantees: org, account, source and type, not
   the data.

### B. Org isolation

1. **One broker.** All events of all orgs, as today. One Trigger and one NATS
   consumer per Subscription, whatever the number of orgs. Decision 2.
2. **Filters only from the controller.** Triggers are created only by our
   controller, nobody else has RBAC for them. For every Subscription that
   isn't a platform admin's, the `pmorg` (and `pmaccount`) filter comes from
   where the Subscription lives or from the provider's bindings, never from
   its spec. This is the first check.
3. **Delivery guard.** One Deployment for the platform. Every Trigger
   delivers to the guard, with the Subscription in the path, not to the
   subscriber's URL. The guard keeps what each Subscription may see (orgs,
   accounts, types) from the same watches the controller uses, and forwards
   to the sink only if the event matches; otherwise it drops the event, counts
   it and alerts. It answers with the sink's status, so Knative's retries and
   the dead letter sink work as today; dead letter sinks go through the guard
   too. This is the second check, written and tested apart from the first.
4. **Signed labels.** The ingress signs `id`, `type`, `source`, `pmorg` and
   `pmaccount` with a key only it and the guard hold (an HMAC in an extension).
   The guard drops events with a missing or wrong signature, so labels can't be
   changed between ingress and delivery, even from inside the cluster. Key
   rotation: two keys valid at a time, from a Secret.
5. **Limits per org.** One stream for all orgs (one day, 512 MiB) means a busy
   org can push other orgs' events out early. The ingress limits events per
   org and per provider; the limits are values of the chart.
6. **NetworkPolicies.** Only Knative's broker filter, which calls Trigger
   subscribers, reaches the guard; only the guard reaches sinks.
7. **Allowlist with orgs.** An entry is either platform wide (only platform
   admin Subscriptions may use it) or for a list of orgs. The controller
   checks it when it creates the Trigger, the guard again before it sends.
   Decision 5.
8. **Isolation e2e** as described above, for platform admin Subscriptions
   with and without `filter.orgs`, plus two negative tests: a Trigger with a
   wrong filter put in by hand, and an event with a changed `pmorg`. The guard
   has to drop both.

### C. Org- and account-level subscriptions

1. **Org export.** A second APIExport, `orgs.eventing.platform-mesh.io`, with
   an `APIExportPolicy` that lets org and account workspaces bind it. Its
   `Subscription` has no `filter.orgs`. Decision 3.
2. **Controller.** Reads the org and, in an account workspace, the account
   from the workspace's `AccountInfo`, never from the spec. Creates the Trigger
   filtered by type, by that org and, for accounts, by that account; the guard
   checks the same.
3. **Account scope.** The ingress adds the extension `pmaccount` to the
   platform's own events about an account (membership, snapshots); provider
   events carry it from part A. An account Subscription gets events of its
   account only, not of its child accounts. Decision 4.
4. **Who may subscribe.** Creating a Subscription needs the owner role on the
   org or account, through the authorization Platform Mesh already does for
   workspace resources. Members can read them.
5. **Types an org may see.** Its own platform events (`io.platform-mesh.*`),
   and the events of providers it bound. Status says which of the requested
   types are allowed.
6. **Contract and guide.** `pmaccount` in the contract; a subscribing guide
   for org and account admins.

### D. Providers receiving events

1. **Provider Subscriptions.** A provider creates Subscriptions in its own
   workspace, through the export it already binds for publishing. The
   controller gives its Trigger the list of orgs that bound the provider and
   updates it as bindings come and go; the guard checks against the
   bindings, not the Trigger. A provider used by many orgs keeps one Trigger
   per Subscription. Knative checks a Trigger's org list one entry at a time
   for every event (spike: 5000 orgs cost 1.2 cores at 300 events/s, p99
   69 ms; etcd caps a Trigger near 10 000 orgs). Above 500 orgs the Trigger
   filters by type only and the guard, a map lookup, does the org check alone;
   the signed labels still protect it.
2. **Types a provider may get.** Its own types, always. Platform types
   (`io.platform-mesh.*`) and other providers' types only if the org agreed
   in its binding of the provider's export, as a permission claim the
   provider declares. Decision 6.
3. **Account bindings.** A provider bound only in some accounts of an org
   gets events of those accounts only, by `pmaccount`.
4. **Sinks.** A provider's sinks are on the allowlist for the orgs that bound
   it, nothing else.
5. **Sample provider** from part A subscribes to its own events and to
   membership events of one org, e2e with the isolation test.

### Alongside all parts

- **Docs.** Architecture (tenancy section rewritten), contract, provider
  guide, subscribing guide for orgs, accounts and providers (with C and D),
  roadmap.
- **RFC** in `platform-mesh/architecture`, written from the ADRs, the contract
  and the architecture doc: provider publishing, tenancy and who may
  subscribe are part of what it agrees. Agreed before part A is released.
- **First consumer** confirms the contract for provider events on the
  released module.
- **Releases.** One after part A; B, C and D each get a release if the
  checkpoint decides to build them.

## Decisions

1. **How providers authenticate and declare their types** (for ADR 0002).
   - a) The provider's Kubernetes ServiceAccount token, checked with the
     TokenReview the ingress already does. Works for providers in the
     platform's runtime cluster, which is where `ManagedProvider` puts them.
   - b) A token of the provider's kcp workspace. Works for providers
     anywhere, but the ingress has to validate kcp tokens.
   - c) A client credentials token from the platform's IdP.

   For the declaration, a resource in the provider's workspace names the
   sender and the event types. The ingress reads it through the eventing
   export; which orgs and accounts bound the provider it reads as in
   Decision 7.

   **Proposal: a)** for this milestone, b) or c) once a provider outside the
   runtime cluster needs it. The ADR decides.
2. **How orgs are kept apart.**
   - a) One broker; the controller sets every org filter, a delivery guard
     checks every event again, the ingress signs the labels.
   - b) An admin broker for platform admins, plus a broker per org that has
     subscribers.
   - c) A broker per org for everyone.

   **Decided: a)**: there can be very many orgs. b) and c) cost a namespace,
   broker and JetStream stream per org, c) also a Trigger per org for every
   admin Subscription. a) costs one guard Deployment and one more hop,
   whatever the number of orgs, and a leak still needs two independent
   mistakes. What we give up: the isolation is in our code, not in
   Kubernetes (no NetworkPolicy or Knative EventPolicy per org), so the
   controller and the guard need the negative e2e tests; and orgs share one
   stream, which the ingress limits per org. b) stays the way out if an org
   needs isolation of its own later (compliance, its own NATS).
3. **The org Subscription API.**
   - a) A second export with its own `Subscription` type, without
     `filter.orgs`.
   - b) The same type, `filter.orgs` rejected outside the eventing workspace.

   **Proposal: a)**: a field that isn't there can't be misused, and the admin
   API stays unchanged.
4. **Account-level.**
   - a) Only the account itself.
   - b) The account and all child accounts.

   **Proposal: a)** now. b) needs the account tree in the ingress or the
   filter, and can come later without a contract change.
5. **Sinks for orgs.**
   - a) The platform operator lists hosts per org on the allowlist.
   - b) Orgs register their own hosts, the operator approves.

   **Proposal: a)** now, b) once there are many orgs.
6. **Platform and foreign types for providers.**
   - a) Through permission claims on the provider's export, accepted per
     binding.
   - b) A list the platform operator keeps per provider.
   - c) Providers get their own types only.

   **Proposal: a)**: the org decides what it shares, with the kcp mechanism
   it already uses to accept a provider.
7. **How eventing learns which orgs and accounts bound a provider.** Orgs
   don't bind the eventing export, so eventing can't see their bindings
   through it. Tested on a local platform-mesh with the httpbin provider,
   bound in an org and in one of its accounts:
   - a) The provider's own export. Eventing watches APIBindings through the
     provider's APIExport and sees only the bindings to it, read-only. Each
     provider has to grant eventing's workspace read access to its export,
     otherwise 403. The account's org still has to come from the core export.
   - b) The core export (`core.platform-mesh.io`). It already claims
     `apibindings` in every org and account, and eventing already has a
     connection to it. One watch sees all bindings, nothing to set up per
     provider, and the same connection answers which org an account belongs
     to (`AccountInfo`). Without `readOnly` the connection could also
     create and delete APIBindings in every org; with it (platform-mesh#591)
     it gets only get, list and watch.
   - c) Orgs bind an eventing export that claims `apibindings`. Every org
     would have to bind eventing and accept the claim before its providers'
     events work.
   - d) An identity that reads APIBindings in all workspaces. Far more than
     eventing needs.
   - e) An index of which org uses which provider, kept by core or the
     marketplace. There is none today.
   - f) The provider says which orgs it serves. That is what the ingress has
     to check, so it can't be the source.

   Both a) and b) saw a bind and an unbind, in the org and in the account,
   in under half a second.

   **Decided: b)**: providers stay as they are (an APIExport and their
   metadata, nothing for eventing), it covers orgs and accounts, and it is
   the view core already has of what an org binds. Eventing's core
   connection is read-only: with `readOnly` it still lists and watches
   APIBindings and reads `AccountInfo`, while create, patch and delete get
   403 (tested locally). The install sets it where the operator supports it.
   a) is the way out if a security review says no.

## Order

1. Decision 1 and ADR 0002, then part A and a release. Runs in parallel with
   milestone 5.
2. Checkpoint: B, C and D are shown with the numbers above, and it is decided
   whether and when to build them.
3. If built: decisions 3 and 5, then part B with its e2e and negative tests.
   The spike (branch `spike/delivery-guard`, `spike/README.md`, not pushed
   yet) already measured the guard: up to 600 events/s on one kind node it
   adds about 0.5 ms at p50 and 0.7 ms CPU per event, isolation held with a
   wrong Trigger filter, and forged or changed labels were dropped. Open:
   several guard replicas, slow sinks behind the guard, a multi-node cluster.
4. Part C, then decision 6 and part D.

The RFC goes alongside 1; it has to be agreed before part A is announced.

Not in this milestone: the first consumer's events (milestone 5), subscriptions
for child accounts, kcp resource events, an inbound data-deletion event,
continuous programs, bridges to message buses.
