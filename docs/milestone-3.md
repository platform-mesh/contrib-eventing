# Milestone 3

Goal: Keycloak events reach subscribers the same way membership events do:
login, user created, user deleted. Snapshots get names.

Draft for discussion. It builds on milestone 2 (snapshots, account path).

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

1. **Keycloak access.** A client with a service account for the adapter, see
   decision 1.
2. **Event store per realm.** Store `LOGIN` and `REGISTER` user events and the
   admin events for users, with a retention time. When security-operator
   updates a realm it only sends the fields it manages, and Keycloak leaves
   fields that are not sent as they are (checked in the 26.7.2 source), so it
   won't switch the store off again. Who switches it on: decision 2.
3. **Events.** New types next to the membership ones, same envelope (`source`
   per org, `pmorg`, `user.id` as in the contract):
   - `io.platform-mesh.iam.user.loggedin.v1alpha1` from `LOGIN`
   - `io.platform-mesh.iam.user.created.v1alpha1` from `REGISTER` and admin
     `CREATE` on a user (invites)
   - `io.platform-mesh.iam.user.deleted.v1alpha1` from admin `DELETE` on a user

   No IP address or session id in the events. Keycloak events carry the
   Keycloak user id and the username. security-operator creates every realm
   with `registrationEmailAsUsername`, so the username is the email, which is
   `user.id` with the default claim. With the `sub` claim, `user.id` is the
   Keycloak user id.
4. **Names in snapshots.** The snapshot event from milestone 2 gets first and
   last name, read with `view-users` and cached.
5. **Docs and test.** Contract, subscribing guide, OpenAPI spec for the new
   types; e2e logs a test user in and checks the event.

## Decisions

Each with the options and what we propose. Points 1 and 2 touch
security-operator, so its owners have to agree.

1. **Keycloak access.** Keycloak creates a management client in `master` for
   every realm, and the `master` admin role picks up its roles automatically
   (Keycloak 26.7.2 source, `RealmManager`). Narrow roles (`view-events`,
   `view-users`, `manage-events`) have to be granted again for every new realm.
   - a) Admin role in `master`, through security-operator's init container,
     which already creates the clients for security-operator and iam-service
     and gives them admin. One more entry in its client list.
   - b) Narrow roles per realm, granted by security-operator when it creates a
     realm. Least privilege, but a code change in security-operator.
   - c) Narrow roles per realm, granted by our install script. Breaks for every
     org created later.

   **Proposal: a) now**, like the other Platform Mesh components, and b) as a
   follow-up with the security-operator owners.
2. **Who switches the event store on.**
   - a) The adapter, for every realm it finds. It only adds the types it needs
     and keeps what others configured (the prototype does that).
   - b) security-operator, behind a flag, when it creates or updates a realm.

   **Proposal: a)**, it works with a) of decision 1 and needs no change in
   security-operator; we ask its owners to accept it.
3. **The `welcome` realm.** Users register and first log in there, before they
   belong to an org, so its events have no org.
   - a) Skip it. Every event keeps `pmorg`, and org filters work as before.
     We lose self-registrations as `user.created`; invites still show up in the
     org's realm.
   - b) Send its events without `pmorg`. Needs a contract change and
     subscribers have to cope with events without an org.

   **Proposal: a)**, `master` is skipped as well.
4. **User created and deleted.** The first consumer's fork sends after
   membership changes and logins, not when users are created or deleted.
   - a) Logins only in this milestone.
   - b) All three.

   **Proposal: b), in that order**: logins first, created and deleted last, so
   they can move out if time runs short.
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

   **Proposal: b)**: the RFC has to be agreed before the module is published,
   and provider publishing is its own design (auth for senders outside the
   cluster).

## Order

1. Agree decisions 1 and 2 with the security-operator owners; set up access
   and the event store.
2. Logins end to end: port the prototype to one stream for all orgs and the
   current contract.
3. Names in snapshots.
4. User created and deleted.
5. Docs and e2e alongside each step.

Not in this milestone: chart and `ManagedProvider` install, durable broker
(milestone 4), org-level subscriptions.
