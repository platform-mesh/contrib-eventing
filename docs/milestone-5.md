# Milestone 5

Goal: Platform Mesh sends every event the first consumer's fork of iam-service
sends, so they can drop the fork. User created and deleted wait for the `User`
CRD of ADR 018, see decision 1.

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
   and `lastName`, read with `view-users` through the cache that snapshots
   already use. No new access.
2. **Email in member lists.** Each member in `account.members` gets an
   optional `email` next to `user.id`. It matters when `user.id` is the
   Keycloak id (`sub` claim). Same source as the names.
3. **Member list on first login.** When a user logs in for the first time, the
   adapter sends `account.members` for every account they belong to, so names
   arrive without waiting for the next change. See decision 4.
4. **Continuous programs.** No new event. A program is an account under a fixed
   parent account, and subscribers tell programs apart by `accountPath`.
   Documented in the subscribing guide. See decision 2.
5. **User created and deleted.** Types reserved in the contract draft, nothing
   sent yet. See decision 1.
6. **Docs and test.** Contract, subscribing guide and OpenAPI spec for the new
   fields; e2e checks email and names in logins and the first-login list.

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

   **Proposal: a)**, with b) ready if the first consumer can't wait. The types
   `io.platform-mesh.iam.user.created.v1alpha1` and
   `io.platform-mesh.iam.user.deleted.v1alpha1` go into the contract draft now
   with the fields the fork sends (`user.id`, email and names on created), so
   any source fills the same events.
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
     send a snapshot per account. Needs the set of users already seen.
   - b) Leave it. Names arrive with the next change of the account.

   **Proposal: a)**.

## Without Keycloak

Logins, names, emails and option b) of decision 1 come from Keycloak and only
exist with the bundled Keycloak. With another OIDC provider subscribers get
membership events and snapshots without names. That is documented as a known
limit.

## Order

1. Email and names in logins and member lists.
2. Member list on first login.
3. Continuous programs in the subscribing guide.
4. Contract draft for user created and deleted.
5. Docs and e2e alongside each step.

Not in this milestone: sending user created and deleted (waits for ADR 018,
or decision 1 b), account and resource events, notifications.
