# ADR 0001: Scope and shape of the eventing module

| Status  | Proposed   |
|---------|------------|
| Date    | 2026-10-23 |

## Context and Problem Statement

Changes in Platform Mesh happen in OpenFGA (memberships), Keycloak (users, logins), kcp (resources) and provider backends. No component tells others about these changes, so every consumer polls each system and builds its own mapping, retries and tenancy handling.

Emitting membership events from iam-service is not enough: it misses every write that does not go through iam-service, such as the OpenFGA proxy, automatic admin assignment and operators ([backlog#367](https://github.com/platform-mesh/backlog/issues/367)).

A [spike](https://github.com/platform-mesh/backlog/issues/421) showed that subscriptions declared in kcp can drive Knative Eventing for delivery, across shards and with isolation between orgs. It also showed that the Knative broker accepts forged events and that a default install adds nine pods.

This ADR fixes the scope and shape of the first milestone so implementation can start.

## Decision

1. **Separate, optional module.** Eventing lives in this repository and changes no existing Platform Mesh component. It observes the system from outside, following [RFC 008](https://github.com/platform-mesh/architecture/blob/main/rfc/008-platform-mesh-modularization.md).
2. **Sources.** Components that can emit events publish them directly. An adapter exists only for systems that cannot. In the first milestone that is OpenFGA, read through its changelog API, which sees every write regardless of who made it.
3. **Subscriptions in kcp, for platform admins.** Platform admins declare a `Subscription` in the eventing workspace; one stream covers all orgs, and each event names its org. Org-level subscriptions are not in the first milestone.
4. **Delivery through Knative, hidden behind our API.** Fan-out, filtering, retries and dead letters come from Knative Broker and Trigger. Consumers never create Knative objects, so the backend can be replaced later.
5. **Own ingress.** All events enter through an ingress that authenticates the sender before they reach the broker. It is reachable through the platform's Gateway API. Subscription targets are limited by an allowlist that the platform operator controls.
6. **One engine-neutral contract.** Events are CloudEvents with the types and fields in the [contract](../contract.md). Users are identified by the user claim the platform admin configured (`email` by default). No OpenFGA identifiers appear in events.
7. **First milestone scope.** Membership events from OpenFGA. Keycloak events (login, user created) come later. A publish path for providers comes with the installable module in milestone 4.

## Considered Options

### Delivery

| Option | Good | Bad |
| --- | --- | --- |
| A. Knative behind our Subscription API (chosen) | Retries, fan-out and dead letters exist and are maintained upstream. The spike proved it. | Extra pods for adopters who enable eventing. Knative's tenancy is the namespace, so we map orgs to namespaces. |
| B. Own small dispatcher | Smallest footprint, no extra dependency | We maintain delivery, retries and dead letters ourselves |
| C. Own dispatcher with Knative as an optional backend | Most flexible | Two delivery paths to build and test |

Because Knative stays behind our API, moving to B or C later changes nothing for consumers.

### Sources for membership events

| Option | Good | Bad |
| --- | --- | --- |
| Read the OpenFGA changelog (chosen) | Sees every write, no code in writers | Polling; membership is shaped by the OpenFGA model |
| Emit from iam-service | Simple | Misses writes from other paths, adds eventing code to a core service |
| Database change capture on OpenFGA | Real time | Depends on OpenFGA's internal schema |

### Security

| Option | Good | Bad |
| --- | --- | --- |
| Own authenticated ingress (chosen) | Works with every Knative version, checks the org of the sender | One more component |
| Knative OIDC | Built in | Alpha |
| NetworkPolicy only | No code | Does not stop a compromised workload in an allowed namespace |

## Consequences

- Good, because Platform Mesh works unchanged without the module, and adopters opt in.
- Good, because membership events cover every OpenFGA write, not only those through iam-service.
- Good, because consumers depend on the contract and the Subscription API, not on Knative.
- Bad, because adopters who enable eventing also run Knative Eventing (seven pods for a minimal install) and need a durable broker class for production.
- Bad, because the OpenFGA adapter polls, so events arrive with a delay of up to one poll interval.
- Neutral, because a role change arrives as two events and account deletion as many revokes. The contract documents this rather than hiding it.

## Open Questions

- ~~Do consumers need snapshots in addition to changes?~~
  Yes. The adapter sends a members snapshot after each change of an account.
- ~~Which durable broker class is the default for production?~~
  The channel based broker on a NATS JetStream channel.
- ~~How does the module map a kcp workspace to its org name without admin access?~~
  A permission claim on `logicalclusters` gives the controller the workspace path.

## References

- Epic: [platform-mesh/backlog#323](https://github.com/platform-mesh/backlog/issues/323)
- Spike: [platform-mesh/backlog#421](https://github.com/platform-mesh/backlog/issues/421)
- [ADR 009: Repository tiers and `contrib-` prefix](https://github.com/platform-mesh/architecture/blob/main/adr/009-repository-tiers-and-contrib-prefix.md)
- [RFC 008: Platform Mesh modularization](https://github.com/platform-mesh/architecture/blob/main/rfc/008-platform-mesh-modularization.md)
