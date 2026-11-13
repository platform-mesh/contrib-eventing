# Roadmap

Each milestone ends with something that runs on a local kind setup.

1. **Membership events.** The OpenFGA adapter publishes `membership.granted`
   and `membership.revoked` for all orgs ([contract](contract.md)). Platform
   admins declare Subscriptions in kcp, Knative delivers to their HTTP endpoints,
   and an ingress, reachable through the platform's Gateway, authenticates every
   sender before an event reaches the broker. One script adds all of this to a
   local Platform Mesh.
2. **Ready for a first consumer.** Members snapshots and account paths in the
   events, subscription targets limited by an allowlist the platform operator
   controls, replies from subscribers blocked, a durable channel (NATS
   JetStream) instead of the in-memory one, timeouts and dead letter sinks.
   The plan and its reasons: [milestone 2](milestone-2.md).
3. **Logins and names.** Login events from the Keycloak event store, and first
   and last names in member snapshots. security-operator provides the event
   store and read-only Keycloak access. Details: [milestone 3](milestone-3.md).
4. **Installable module and provider events.** Published images, chart and
   OCM component, install through `ManagedProvider` on any Platform Mesh, CI.
   Providers publish their own events through the ingress. Details:
   [milestone 4](milestone-4.md).
5. **Later.** User created and deleted from the `User` CRD of ADR 018, for any
   IdP. Org- and account-level subscriptions, kcp resource events, more than
   one kcp shard.

Delivery stays HTTP to the subscriber in all of these. Bridges to GCP Pub/Sub,
Kafka and the like run on the subscriber side.
