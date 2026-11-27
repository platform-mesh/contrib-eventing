# Milestone 5

Goal: Platform Mesh sends every event the first consumer's fork of iam-service
sends, so they can drop the fork.

It builds on milestones 2 to 4 (snapshots, logins, names, the installable
module).

## What the first consumer needs

Everything their fork sends today, not a subset. After milestone 4 we cover
membership changes and logins. Still missing or thinner than in the fork:

| Fork sends | After milestone 4 | Gap |
| --- | --- | --- |
| login | `user.loggedin` | only `user.id`, no email and no names |
| full member list of an account | `account.members` plus `granted` / `revoked` | no separate email per member; no list when an invited user logs in for the first time, so their name shows up only with the next change |
| member list of a continuous program | `account.members` if the program is an account | subscribers can't tell a program from another account |
| user created | none | decision 1 |
| user deleted | none | decision 1 |
| notifications (welcome, joined, role changed) | none | decision 3 |

The fork also defines events for single user adds and removes, but never sends
them, so there is nothing to replace. `granted` and `revoked` carry the same
facts.

The fork does not send account created or deleted; that stays under Later in
the [roadmap](roadmap.md).

## Work

1. **Email and names in logins.** `user.loggedin` gets `email`, `firstName`
   and `lastName`, read with `view-users` like the names in snapshots. No new
   access, but small code changes: the Keycloak client doesn't read the email
   yet, and its cache only keeps users that have names.
2. **Email in member lists.** Each member in `account.members` gets an
   optional `email` next to `user.id`. Under the `email` claim it is `user.id`
   itself, so it works without Keycloak. Under the `sub` claim it comes from
   Keycloak with the names.
3. **Member list on first login.** When a user logs in for the first time, the
   OpenFGA adapter sends `account.members` for every account they belong to,
   so names arrive without waiting for the next change. See decision 4.
4. **Continuous programs.** No new event. A program is an account under a fixed
   parent account, and subscribers tell programs apart by `accountPath`.
   Documented in the subscribing guide. See decision 2.
5. **User created and deleted.** Sent by the OpenFGA adapter when a user gets
   their first role in an org or loses their last one, switched to the `User`
   CRD once it exists. See decision 1.
6. **Contract.** All new fields are optional and filled where the IdP source
   can. Any later source (another IdP, the `User` CRD) fills the same fields,
   so subscribers don't change. The contract also says what created and
   deleted mean and who deletes personal data, see decision 5.
7. **Docs and test.** Contract, subscribing guide and OpenAPI spec for the new
   fields; e2e checks email and names in logins, the first-login list and
   user created.

## Decisions

Each with the options and what we propose.

1. **User created and deleted.** The only source that works with any IdP is
   the `User` CRD of ADR 018
   ([platform-mesh/architecture#62](https://github.com/platform-mesh/architecture/pull/62)).
   It is still a draft with no owner and no date. Since
   platform-mesh/platform-mesh#687 Platform Mesh also runs without Keycloak,
   so a Keycloak-only source misses those installs.
   - a) Wait for the `User` CRD and watch it like `AccountInfo`.
   - b) Read Keycloak admin events (`CREATE` and `DELETE` on users) and
     `REGISTER` through the existing Keycloak adapter, and switch to the CRD
     once it exists. Needs admin events switched on per realm, which stores
     every admin operation in the realm. Same retention gap as logins.
   - c) Compare the users of every realm with a stored list on each poll. No
     gap after outages, but state per user and one poll interval late.
   - d) Derive them from membership in OpenFGA: the first role of a user in
     an org is created, removing their last role is deleted. No Keycloak
     settings, works with any IdP.

   **Proposal: d)**, as a stopgap until the `User` CRD exists. Without these
   events the first consumer can't drop the fork, and ADR 018 has no date.
   OpenFGA stores permissions, not users, so this is not the right source in
   the long run. But the fork means the same thing by created ("first seen in
   this org", see decision 5), and every source we have today is a stand-in:
   b) and c) only work with Keycloak, and b) also needs new realm settings
   and stores more personal data in Keycloak. The types
   `io.platform-mesh.iam.user.created.v1alpha1` and
   `io.platform-mesh.iam.user.deleted.v1alpha1` carry the fields the fork
   sends (`user.id`, email and names on created), so the `User` CRD later
   fills the same events and subscribers don't notice the switch.
2. **Continuous programs.**
   - a) An account under a fixed parent, for example `continuous-programs`.
     Membership events already fire for it. No code.
   - b) As a) plus an `accountType` field in membership events, read from
     `AccountInfo`. Only helps once account-operator can create other account
     types than `org` and `account`.
   - c) A provider resource with its own members. Needs a new event type and
     reading memberships of provider types.

   **Proposal: a)**, b) later if account types arrive.
3. **Notifications.** The fork sends welcome, joined and role changed
   notifications to a separate topic. They are off by default in its chart.
   - a) No notification events. Subscribers build them from `user.created`,
     `granted` and `revoked`, which carry the same facts.
   - b) Separate notification types.

   **Proposal: a)**.
4. **Member list on first login.** Platform Mesh has no event when an invite
   is accepted, the OpenFGA tuple exists from the invite on.
   - a) On the first `LOGIN` of a user, list their accounts in OpenFGA and
     send a snapshot per account. The OpenFGA adapter reads the logins itself
     with the Keycloak access it already has. It is the first login when the
     event store has no earlier `LOGIN` of that user, so no list of users
     already seen.
   - b) Leave it. Names arrive with the next change of the account.

   **Proposal: a)**. The fork sends the list on every login that accepts
   pending invites. Here the OpenFGA tuple exists from the invite on, so the
   first login is the matching trigger. Under the `sub` claim invited members
   are stored under their email, so the first-login list doesn't find them by
   the Keycloak id. That is documented as a known limit.
5. **Meaning of user created and deleted, and personal data.**
   - created: the user got their first role in this org, usually through an
     invite. A user in two orgs gets two `created` events.
   - deleted: the user lost their last role in this org. It is not a request
     to purge their data everywhere, since they may still be in other orgs.
   - Email and names are only on logins, member lists and created, never on
     `granted` or `revoked`. Subscribers that store them are responsible for
     deleting them.

## Without Keycloak

Logins, names, emails under the `sub` claim and the first-login list come
from Keycloak and only exist with the bundled Keycloak. All of it stays in the
optional Keycloak parts of this module, with no Keycloak changes in Platform
Mesh itself. With another OIDC provider subscribers get membership events,
snapshots and user created and deleted, with emails under the `email` claim
but without names. That is documented as a known limit.

## Order

1. Email and names in logins and member lists.
2. Member list on first login.
3. User created and deleted.
4. Continuous programs in the subscribing guide.
5. Docs and e2e alongside each step.

Not in this milestone: the `User` CRD as a source (waits for ADR 018), account
and resource events, notifications.
