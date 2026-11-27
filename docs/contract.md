# Event contract

This is what subscribers can rely on. It is a draft (`v1alpha1`) and may still change.

Every event is a [CloudEvent 1.0](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md) delivered over HTTP. The contract does not depend on which system produced the change: today membership and user created and deleted events come from OpenFGA and login events from Keycloak, but another adapter could produce the same events from a different authorization engine or identity provider.

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

Users are identified the way the platform admin configured Platform Mesh: by the user claim it authorizes with (`email` by default, or `sub`). `user.id` is the value Platform Mesh authorizes the user with, so with the default it is the email. Invitations are always written with the email, so with the `sub` claim an invited user who never logged in appears with their email.

## Members snapshot

Sent after the members of an account changed, and after a member's first
login. It carries the full list as it is in the authorization engine when the
event is built, so a subscriber that keeps member lists can replace its copy
instead of applying changes.

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
      { "user": { "id": "alice@example.com", "email": "alice@example.com", "firstName": "Alice", "lastName": "Liddell" }, "roles": ["owner"] },
      { "user": { "id": "bob@example.com", "email": "bob@example.com" }, "roles": ["member"] }
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
  never carry names or emails.
- **Email.** `user.email` is the user's email. With the `email` claim it is
  `user.id` and always there, Keycloak access or not. With `sub` it comes from
  Keycloak together with the names, by the same rules: missing without Keycloak
  access, for users Keycloak doesn't know by that id, and for all members when
  `errors` has `namesUnavailable`.
- **Errors.** `errors` is only there when something went wrong while the
  snapshot was built. It lists codes. `namesUnavailable`: the names could not be
  read from Keycloak, so no member has one, though they may have one in Keycloak.
  With the `sub` claim the emails are missing too.
  It is not set when eventing has no Keycloak access, since then no snapshot has
  names. The members list is complete either way. A later snapshot without
  `errors` has the names again.
- **On change and after first logins.** A snapshot is sent when the account's
  members change. It is also sent for every account a user has a role on after
  their first login to the org, so an invited user's names arrive once they
  have logged in and set them. Keycloak keeps logins for seven days by default,
  so the first login after a longer pause counts as first, too, as does every
  user's first login after eventing was installed. These need Keycloak access.
  With the `sub` claim a first login finds only the roles written under the
  user's Keycloak id, not those still under the email they were invited
  with. A new Subscription, or one that missed a snapshot, gets an
  account's list with that account's next change or first login.

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
    "user": {
      "id": "alice@example.com",
      "email": "alice@example.com",
      "firstName": "Alice",
      "lastName": "Liddell"
    }
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
| `user.email` | no | the user's email, if they have one |
| `user.firstName`, `user.lastName` | no | the user's names, if they have set them |

- **Profile as of the login.** The adapter reads the user from Keycloak for
  every login, so the email and names are current when the event is built, not
  cached. If Keycloak can't be read, the login waits and goes out with the next
  successful read. A user deleted before that read is sent with `user.id` only.

- **No IP address, session or client.** Keycloak records them, the event leaves them out.
- **Org realms only.** Logins to the `welcome` and `master` realms are not published, because they have no org.
- **Only with the bundled Keycloak, for now.** The type has no Keycloak details, so an adapter for another identity provider could send the same events, but today they only come from Keycloak's event store.
- **Duplicates** share the `id`; drop them.
- **Logins can be lost.** Keycloak keeps stored events for seven days by default. If the adapter is down for longer, the logins in between are never published.
- **Nothing from before eventing was installed.** Logins in an org that existed at install time count from the install; an org created later counts from its first login.

## User created and deleted

Sent when a user joins or leaves an org.

| Type | Meaning |
| --- | --- |
| `io.platform-mesh.iam.user.created.v1alpha1` | The user got their first role in the org, on any of its accounts. An invite gives a role, so it counts |
| `io.platform-mesh.iam.user.deleted.v1alpha1` | The user's last role in the org was removed |

```json
{
  "specversion": "1.0",
  "id": "5e2d8c1b7a6f4e3d9c0b1a2f3e4d5c6b",
  "source": "platform-mesh.io/orgs/acme/iam",
  "type": "io.platform-mesh.iam.user.created.v1alpha1",
  "subject": "users/alice@example.com",
  "time": "2026-10-01T09:10:02.512Z",
  "pmorg": "acme",
  "datacontenttype": "application/json",
  "data": {
    "org": "acme",
    "user": {
      "id": "alice@example.com",
      "email": "alice@example.com",
      "firstName": "Alice",
      "lastName": "Liddell"
    }
  }
}
```

A deleted user has `"user": { "id": "alice@example.com" }` and nothing else.
Attributes are as for login events; `time` is when the role changed.

| Field | Required | Meaning |
| --- | --- | --- |
| `org` | yes | org name |
| `user.id` | yes | the user, by the same rule as in membership events |
| `user.email`, `user.firstName`, `user.lastName` | no | only on created, by the same rules as in member lists. Invited users have no names yet |
| `errors` | no | only on created, as in member lists. The created event isn't sent again with names; they arrive with the next member list and the user's first login |

- **Joining and leaving, not accounts.** A user in two orgs gets a created
  event in each. Further roles in the same org, or losing one of several, send
  nothing; membership events and snapshots say who has which role.
- **Deleted means removed from this org.** It is not a request to erase the
  person everywhere. Subscribers who store personal data about the user are
  responsible for deleting it as their own obligations require; the event is
  the signal to check.
- **From membership, for now.** Today they are derived from the roles in
  OpenFGA and sent with the `granted` or `revoked` that made the change, with
  an `id` of their own that the same change always gives. Once the `User`
  resource of ADR 018 exists they come from it, with the same types and fields.
- **Same limits as membership.** Org deletion sends no deleted events, and
  nothing from before eventing was installed is sent.

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
