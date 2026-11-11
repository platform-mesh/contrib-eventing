# Event contract

This is what subscribers can rely on. It is a draft (`v1alpha1`) and may still change.

Every event is a [CloudEvent 1.0](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md) delivered over HTTP. The contract does not depend on which system produced the change: today membership events come from OpenFGA and login events from Keycloak, but another adapter could produce the same events from a different authorization engine or identity provider.

## Membership events

Sent when a user gets or loses a role on an account.

| Type | Meaning |
| --- | --- |
| `io.platform-mesh.iam.membership.granted.v1alpha1` | The user now has the role |
| `io.platform-mesh.iam.membership.revoked.v1alpha1` | The user no longer has the role |

### Example

```json
{
  "specversion": "1.0",
  "id": "25fdb262e1c7303601770040f01083d9",
  "source": "platform-mesh.io/orgs/acme/iam",
  "type": "io.platform-mesh.iam.membership.granted.v1alpha1",
  "subject": "accounts/a5zld11wopsu1uea/team-a",
  "time": "2026-10-01T09:12:44Z",
  "pmorg": "acme",
  "datacontenttype": "application/json",
  "data": {
    "org": "acme",
    "account": "team-a",
    "accountId": "a5zld11wopsu1uea/team-a",
    "accountPath": "root:orgs:acme:team-a",
    "role": "member",
    "user": { "id": "alice@example.com" }
  }
}
```

### Attributes

| Attribute | Value |
| --- | --- |
| `id` | Unique per change and the same on every redelivery. Use it to drop duplicates. Do not parse it. |
| `source` | `platform-mesh.io/orgs/<org>/iam` |
| `type` | one of the types above |
| `subject` | `accounts/<accountId>` |
| `time` | when the change happened |
| `pmorg` | the org name, so subscribers can filter without reading `data` |

### Data

| Field | Required | Meaning |
| --- | --- | --- |
| `org` | yes | org name |
| `account` | yes | account name. Nested accounts can share a name, so don't use it as a key. |
| `accountId` | yes | the account, unique in the platform: the logical cluster of the workspace that holds the Account object, and the account name. It is the same pair the account's `AccountInfo` shows as `originClusterId` and `name`. |
| `accountPath` | no | the account's kcp workspace path, for example `root:orgs:acme:team-a`. The iam-service API addresses accounts by it. Missing if the account was deleted before eventing saw it, for example while the adapter was restarting. |
| `role` | yes | Platform Mesh role, for example `owner` or `member` |
| `user.id` | yes | the user as Platform Mesh identifies them, see below |

Users are identified the way the platform admin configured Platform Mesh: by the user claim security-operator uses (`--user-claim`, `email` by default). `user.id` is the value Platform Mesh authorizes the user with, so with the default it is the email. Invitations are always written with the email, so with the `sub` claim an invited user who never logged in appears with their email.

## Members snapshot

Sent after the members of an account changed. It carries the full list as it
is in the authorization engine when the event is built, so a subscriber that
keeps member lists can replace its copy instead of applying changes.

| Type | Meaning |
| --- | --- |
| `io.platform-mesh.iam.account.members.v1alpha1` | The account's members and their roles now |

```json
{
  "specversion": "1.0",
  "id": "7c1d0e2a9b3f45e8a1c2d3e4f5a6b7c8",
  "source": "platform-mesh.io/orgs/acme/iam",
  "type": "io.platform-mesh.iam.account.members.v1alpha1",
  "subject": "accounts/a5zld11wopsu1uea/team-a",
  "time": "2026-10-01T09:12:44Z",
  "pmorg": "acme",
  "datacontenttype": "application/json",
  "data": {
    "org": "acme",
    "account": "team-a",
    "accountId": "a5zld11wopsu1uea/team-a",
    "accountPath": "root:orgs:acme:team-a",
    "members": [
      { "user": { "id": "alice@example.com", "firstName": "Alice", "lastName": "Liddell" }, "roles": ["owner"] },
      { "user": { "id": "bob@example.com" }, "roles": ["member"] }
    ]
  }
}
```

Attributes and the fields `org`, `account`, `accountId`, `accountPath` and
`user.id` mean the same as for membership events. `time` is when the list was read. `members` is
sorted by `user.id`, `roles` by name. Several changes to one account can lead
to a single snapshot.

- **Keep the newest.** Snapshots can arrive in any order; use the one with the
  latest `time`.
- **Same id, same list.** A retried snapshot keeps its `id` only if the list is
  the same. If the members changed in between, it is a new event with a new `id`.
- **Explicit roles only.** A role given to everyone (`user:*`) is not listed,
  and owners are not listed as members unless they also have the member role.
- **An empty list** means the account has no members, for example after it was
  deleted.
- **Names.** `user.firstName` and `user.lastName` are set when Keycloak knows
  them. They are missing for users who have none, for example invited users who
  haven't set them yet, and for all users where eventing has no Keycloak access.
  They can be up to five minutes old. Names come for all members of a
  snapshot or for none: if Keycloak can't be read in time, the snapshot has no
  names at all and says so in `errors` (below). A missing name therefore does
  not mean it was removed; keep the names you already have. Membership events
  never carry names.
- **Errors.** `errors` is only there when something went wrong while the
  snapshot was built. It lists codes. `namesUnavailable`: the names could not be
  read from Keycloak, so no member has one, though they may have one in Keycloak.
  It is not set when eventing has no Keycloak access, since then no snapshot has
  names. The members list is complete either way. A later snapshot without
  `errors` has the names again.
- **Only on change.** A snapshot is sent when the account's members change. A
  new Subscription, or one that missed a snapshot, gets an account's list with
  that account's next change.

## Login events

Sent after a user logged in to an org's Keycloak realm.

| Type | Meaning |
| --- | --- |
| `io.platform-mesh.iam.user.loggedin.v1alpha1` | The user logged in |

```json
{
  "specversion": "1.0",
  "id": "0b8f6c1d2e3a4f5b8c9d0e1f2a3b4c5d",
  "source": "platform-mesh.io/orgs/acme/iam",
  "type": "io.platform-mesh.iam.user.loggedin.v1alpha1",
  "subject": "users/alice@example.com",
  "time": "2026-10-01T09:12:40.123Z",
  "pmorg": "acme",
  "datacontenttype": "application/json",
  "data": {
    "org": "acme",
    "user": { "id": "alice@example.com" }
  }
}
```

Attributes are as for membership events, with these differences:

| Attribute | Value |
| --- | --- |
| `id` | unique per login and the same on every redelivery |
| `subject` | `users/<user.id>` |
| `time` | when the user logged in |

| Field | Required | Meaning |
| --- | --- | --- |
| `org` | yes | org name |
| `user.id` | yes | the user as Platform Mesh identifies them, by the same rule as in membership events: the value of the user claim, the email by default and the Keycloak user id with `sub` |

- **No IP address, session or client.** Keycloak records them, the event leaves them out.
- **Org realms only.** Logins to the `welcome` and `master` realms are not published, because they have no org.
- **Only with the bundled Keycloak, for now.** The type has no Keycloak details, so an adapter for another identity provider could send the same events, but today they only come from Keycloak's event store. User created and deleted events will come from the `User` resource instead, so they work with any provider.
- **Duplicates** share the `id`; drop them.
- **Logins can be lost.** Keycloak keeps stored events for seven days by default. If the adapter is down for longer, the logins in between are never published.
- **Nothing from before eventing was installed.** Logins in an org that existed at install time count from the install; an org created later counts from its first login.

## What subscribers should expect

- **Duplicates.** Delivery is at least once. Drop events whose `id` you have already seen.
- **No ordering.** A role change arrives as a revoke of the old role and a grant of the new one, in either order.
- **Account deletion** shows up as one revoke per member and a snapshot with an empty list. There is no separate event for it yet.
- **Org deletion** produces no events.
- **Nothing from before eventing was installed.** Older changes are not replayed. Orgs created later are covered from their first change.
- **Account creation** shows up as an owner grant for the creator.

## Open questions

- Is the role list fixed, or can adopters define their own roles?

## Versioning

Proposed rule: the version is part of the type. Adding an optional field keeps the version. Removing or changing the meaning of a field creates a new type version, and both versions are sent during a transition.
