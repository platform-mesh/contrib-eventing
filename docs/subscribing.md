# Receiving events

This is for platform admins who want the membership and login events of the platform
delivered to their own HTTP endpoint.

## 1. Provide an endpoint

Your endpoint receives one CloudEvent per `POST`, in CloudEvents HTTP binary
mode: the attributes come as `ce-*` headers (`ce-id`, `ce-type`, `ce-source`,
`ce-subject`, `ce-time`, `ce-pmorg`), the data as the JSON body.
[api/openapi/subscriber.yaml](../api/openapi/subscriber.yaml) describes the
requests and the answers your endpoint must give; [contract.md](contract.md)
explains the fields.

The rules that matter:

- Answer with a 2xx and an **empty body**, quickly. A body that is not a
  CloudEvent counts as a failed delivery. A body that is a CloudEvent is taken
  as a reply. Replies are blocked and never reach anyone, but Knative counts
  the blocked reply as a failure: with a dead letter sink, every event you
  replied to also goes there, about a minute later, with `knativeerrordest`
  set to the broker.
- 5xx, 408 and 429 are retried with backoff; any other status is not (see
  below).
- The same event can arrive more than once. Drop events whose `ce-id` you have
  already seen. There is no order between events.
- The endpoint must be reachable from inside the cluster, for example as a
  Kubernetes Service. Requests come without credentials.
- Its host must be on the platform operator's allowlist (the controller's
  `--allowed-sink-hosts`: host names, or `*.suffix` for everything below a
  domain). Events carry user ids, so the operator decides where they may go.
  Otherwise the Subscription stays `Ready=False` with reason `SinkNotAllowed`.
  The same goes for the dead letter sink. The local install allows
  `*.eventing-demo-sink.svc.cluster.local`. Redirects are followed, so don't
  answer with one unless you mean to pass the events on.

## 2. Create a Subscription

Subscriptions live in eventing's provider workspace
`root:providers:eventing-<id>`, which only platform admins can reach. With the
repo's scripts on a local setup:

```sh
source hack/lib.sh
ws=$(eventing_workspace)
kcp "$ws" apply -f - <<EOF
apiVersion: eventing.platform-mesh.io/v1alpha1
kind: Subscription
metadata:
  name: my-consumer
spec:
  filter:
    types:
      - io.platform-mesh.iam.membership.granted.v1alpha1
      - io.platform-mesh.iam.membership.revoked.v1alpha1
      - io.platform-mesh.iam.account.members.v1alpha1
      - io.platform-mesh.iam.user.loggedin.v1alpha1
      - io.platform-mesh.iam.user.created.v1alpha1
      - io.platform-mesh.iam.user.deleted.v1alpha1
    orgs: [acme]   # optional; without it you get all orgs
  sink:
    uri: http://my-consumer.my-namespace.svc.cluster.local
  delivery:
    retry: 3
    timeout: PT10S   # optional, per request
    deadLetterSink:  # optional
      uri: http://my-dead-letters.my-namespace.svc.cluster.local
EOF
kcp "$ws" get subscriptions
```

`delivery` is optional; without it you get 10 retries with exponential
backoff from 0.2 seconds. `retry` is the number of retries after the first
failed attempt; `backoffPolicy` (`linear` or `exponential`, default
`exponential`) and `backoffDelay` (ISO 8601, default `PT0.2S`) shape the wait in
between. `timeout` limits a single request and must be more than zero; if Knative rejects
the settings, the Subscription stays `Ready=False` with reason `InvalidDelivery`. An event that still failed after the
last retry goes to `deadLetterSink`, with Knative's `knativeerrordest`,
`knativeerrorcode` and `knativeerrordata` attributes added; when the last try
timed out there was no answer, and these attributes are missing. Without a dead
letter sink it is dropped.

Only answers 5xx, 408 and 429, a sink that cannot be reached and a request
that times out are retried. Without `timeout` a request may take about 30
seconds. Any other 4xx drops the event at once, without the dead letter sink.

```mermaid
flowchart TD
  send[Event sent to your endpoint] --> answer{Your answer}
  answer -- "2xx, empty body" --> done[Delivered]
  answer -- "2xx with a CloudEvent" --> reply["Delivered, the reply is blocked"]
  reply --> dlsset
  answer -- "5xx, 408, 429, timeout,<br/>unreachable, other 2xx body" --> left{Retries left?}
  answer -- other 4xx --> dropped[Dropped]
  left -- yes --> wait[Wait, backoff grows] --> send
  left -- no --> dlsset{Dead letter sink?}
  dlsset -- yes --> dl[Sent to the dead letter sink]
  dlsset -- no --> dropped
```

The Subscription is ready when `READY` is `True`; otherwise `REASON` says what
is missing. Events start arriving with the next change or login. Deleting the
Subscription stops delivery.

## Continuous programs

There is no event type for continuous programs. A program is an account like
any other, created under one fixed parent account per org, for example
`continuous-programs`. Membership events and member lists already cover it.

A subscriber tells programs apart by the account path:

```
root:orgs:<org>:continuous-programs:<program>
```

Anything with the prefix `root:orgs:<org>:continuous-programs:` and exactly one
more segment is a program. Accounts nested below a program would match the
prefix too, so check the segment count. Use `accountId` as the key for a
program, not its name or path: the name can repeat in other orgs, and the
`accountId` doesn't change.

`accountPath` is missing when eventing never saw the account's workspace, for
example for an account deleted while the adapter was down. Such an event can't
be placed by path; keep it by `accountId` if you know the program already, or
drop it.

If you are moving from a member list event that carries `users` with `id`,
`email`, `roles`, `first_name` and `last_name` for a program, the
`account.members` snapshot gives the same:

| Before | Platform Mesh |
| --- | --- |
| the program in the subject | `accountPath`, and `accountId` as the key |
| the tenant | `org`, also in the `pmorg` attribute |
| full list of `users` | `members`, the full list; keep the one with the latest `time` |
| `users[].id` | `user.id`, the platform's user claim, the email by default. Not the old user id |
| `users[].email` | `user.email` |
| `users[].first_name`, `last_name` | `user.firstName`, `user.lastName`, where Keycloak knows them |
| `users[].roles` | `roles`, for example `owner` or `member` |

Invited users are in the list from the invite on, with their email as id and no
names until they log in. Owners of the parent account own the program too, but
the list only has roles given on the program itself.

## Limits

- Events are kept for a day at most. A sink that is down longer misses them.
- Two Subscriptions with the same sink deliver each event twice, with the same
  `id`.
