# Milestone 3

Goal: Keycloak logins reach subscribers the same way membership events do.
Snapshots get names. User created and deleted are not part of this milestone,
see decision 4.

It builds on milestone 2 (snapshots, account path).

## What the first consumer needs

Their fork of iam-service publishes after every login, and their member lists
have names. Both come from Keycloak, which OpenFGA knows nothing about.

## How we read Keycloak

Keycloak has no API to push events out. It can store them per realm, and the
admin API lists the stored events from a given time on (epoch millis,
inclusive, Keycloak 26.7.2). Platform Mesh runs Keycloak 26.7.2 (our cluster,
`keycloak-server-spi-private-26.7.2.jar`).

| Option | How | For | Against |
| --- | --- | --- | --- |
| A. Poll the event store | Like the OpenFGA adapter: one cursor per realm, read new events, publish them. The prototype on `feat/keycloak-login` does this for logins. | No change to Platform Mesh components | Events older than the store's retention are gone, so a long adapter outage leaves a gap; one request per realm per poll |
| B. Event listener in Keycloak | A Keycloak provider that sends each event to our ingress as it happens | No gap, no polling | Changes Platform Mesh's own Keycloak image (`custom-images/keycloak`, today a plain build of 26.8.1) |

Proposal: A for this milestone. B only if the gap or the polling turns out to
be a problem.

## Work

1. **Keycloak access.** A client with a service account for the adapter that
   only has `view-events` and `view-users`, see decision 1.
2. **Event store per realm.** Store `LOGIN` user events only (no `REGISTER`, no
   admin events), with a retention time of 7 days, configurable. When
   security-operator updates a realm it only sends the fields it manages, and
   Keycloak leaves fields that are not sent as they are (checked in the 26.7.2
   source), so it won't switch the store off again. Who switches it on:
   decision 2.
3. **Events.** One new type next to the membership ones, same envelope
   (`source` per org, `pmorg`, `user.id` as in the contract):
   - `io.platform-mesh.iam.user.loggedin.v1alpha1` from `LOGIN`

   The type has no Keycloak details, so an adapter for another IdP can send
   the same events later. No IP address or session id in the events. Keycloak events carry the
   Keycloak user id and the username. security-operator creates every realm
   with `registrationEmailAsUsername`, so the username is the email, which is
   `user.id` with the default claim. With the `sub` claim, `user.id` is the
   Keycloak user id.
4. **Names in snapshots.** The snapshot event from milestone 2 gets first and
   last name, read with `view-users` and cached.
5. **Docs and test.** Contract, subscribing guide, OpenAPI spec for the new
   type; e2e logs a test user in and checks the event.

## Decisions

Each with the options and what we propose. Points 1 and 2 touch
security-operator, so its owners have to agree.

1. **Keycloak access.** Keycloak creates a management client in `master` for
   every realm, and the `master` admin role picks up its roles automatically
   (Keycloak 26.7.2 source, `RealmManager`). Narrow roles (`view-events`,
   `view-users`) have to be granted again for every new realm.
   - a) Admin role in `master`, through security-operator's init container,
     which already creates the clients for security-operator and iam-service
     and gives them admin. One more entry in its client list.
   - b) Narrow roles per realm, granted by security-operator when it creates a
     realm, with a reader client in `master`, behind a flag. Least privilege,
     but a code change in security-operator.
   - c) Narrow roles per realm, granted by our install script. Breaks for every
     org created later.

   **Decided: b)**: security-operator grants a reader client in `master`
   `view-events` and `view-users` per realm behind a flag
   (platform-mesh/platform-mesh#578). The adapter never gets admin or
   `manage-events`. Until #578 lands, a local install step does the same job
   and is marked as a stopgap.
2. **Who switches the event store on.**
   - a) The adapter, for every realm it finds. It only adds the types it needs
     and keeps what others configured (the prototype does that).
   - b) security-operator, behind a flag, when it creates or updates a realm.

   **Decided: b)**: security-operator switches the store on per realm, same
   issue (platform-mesh/platform-mesh#578). The adapter has no
   `manage-events`. Until then a local install step does it, marked as a
   stopgap.
3. **The `welcome` realm.** Users register and first log in there, before they
   belong to an org, so its events have no org.
   - a) Skip it. Every event keeps `pmorg`, and org filters work as before.
     Self-registrations happen there, which only matters once user created
     exists (decision 4).
   - b) Send its events without `pmorg`. Needs a contract change and
     subscribers have to cope with events without an org.

   **Proposal: a)**, `master` is skipped as well.
4. **User created and deleted.** The first consumer's fork sends after
   membership changes and logins, not when users are created or deleted.
   - a) Logins only in this milestone.
   - b) All three.

   **Decided: a)**: user created and deleted move out of this milestone. They
   will come from the `User` CRD of ADR 018 (platform-mesh/architecture#62),
   so they work with any IdP. Login stays per IdP and only exists with the
   bundled Keycloak.
5. **Retention and gaps.** Keycloak deletes stored events after the retention
   time. If the adapter is down longer, those events are lost. Snapshots heal
   member lists, but a lost login stays lost.
   - a) 1 day: little stored personal data, short outages only.
   - b) 7 days: covers a long weekend.
   - c) 30 days: rarely a gap, a month of logins stored per realm.

   **Proposal: b)**, configurable, and documented as a known gap.
6. **Provider publishing and the RFC.** Both are in the epic's definition of
   done and have no milestone yet.
   - a) Add them to this milestone.
   - b) Write the RFC alongside this milestone, provider publishing as
     milestone 4, the installable module moves to 5.
   - c) Both into milestone 4.

   **Decided: c)**: provider publishing and the installable module both go
   into milestone 4, as two parts with their own issues that can run in
   parallel. Provider publishing starts with its own ADR. See
   [milestone 4](milestone-4.md).

## Order

1. Set up access and the event store (stopgap install step until
   platform-mesh/platform-mesh#578).
2. Logins end to end: port the prototype to one stream for all orgs and the
   current contract.
3. Names in snapshots.
4. Docs and e2e alongside each step.

Not in this milestone: user created and deleted (ADR 018), chart and
`ManagedProvider` install, provider publishing (milestone 4), org-level
subscriptions.
