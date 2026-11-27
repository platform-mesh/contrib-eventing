#!/usr/bin/env bash
# End-to-end check against an installation made by hack/install.sh. Leaves
# the installation as it found it.
source "$(dirname "$0")/../hack/lib.sh"
source "$ROOT/hack/steps/00-preflight.sh" >/dev/null

failed=0
pass() { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; failed=1; }

sub=memberships-to-event-display
EVENTING_WS=$(eventing_workspace)
admin() { kcp "$EVENTING_WS" "$@"; }
INGRESS_URL=http://eventing-ingress.$NS.svc.cluster.local
sink_logs() { kind_kubectl -n eventing-demo-sink logs deploy/event-display; }
count() { sink_logs | grep -c "type: io.platform-mesh.iam.membership.$1" || true; }
more_than() { (($(count "$1") > $2)); }
# The last event of a type, as event-display prints it.
last_event() { sink_logs | grep -A23 "type: io.platform-mesh.iam.membership.$1" | tail -24; }
snapshots() { sink_logs | grep -c "type: io.platform-mesh.iam.account.members" || true; }
more_snapshots_than() { (($(snapshots) > $1)); }
# The data of the last members snapshot of an account.
last_snapshot() {
  sink_logs | awk -v subject="subject: accounts/$1" '
    function done() { if (index(buf, "type: io.platform-mesh.iam.account.members") && index(buf, subject "\n")) last = buf }
    /cloudevents.Event/ { done(); buf = "" }
    { buf = buf $0 "\n" }
    END { done(); printf "%s", last }' | sed '1,/^Data,/d'
}
# roles <account id> <user>: the user's roles in the last snapshot, as JSON.
roles() { last_snapshot "$1" | jq -c --arg u "$2" '[.members[] | select(.user.id == $u) | .roles[]]'; }
add_member() { "$ROOT/hack/add-member.sh" "$@" >/dev/null; }
# Login events. event-display prints the type first, so the context and the data follow.
logins() { sink_logs | grep -c "type: io.platform-mesh.iam.user.loggedin" || true; }
more_logins_than() { (($(logins) > $1)); }
last_login() { sink_logs | grep -A23 "type: io.platform-mesh.iam.user.loggedin" | tail -24; }
subscription_ready() { [[ $(admin get subscription $sub -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}') == True ]]; }

log "The ManagedProvider installed eventing"
[[ $(kind_kubectl -n "$NS" get managedprovider "$PROVIDER" -o jsonpath='{.status.phase}') == Ready ]] &&
  pass "ManagedProvider $PROVIDER Ready" || fail "ManagedProvider $PROVIDER not Ready"
[[ $(admin get apibinding eventing.platform-mesh.io -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}') == True ]] &&
  pass "kcp-setup bound the eventing API in $EVENTING_WS" || fail "APIBinding not Ready in $EVENTING_WS"

log "The platform admin's Subscription is ready"
subscription_ready && pass "Ready in $EVENTING_WS" || fail "Subscription not ready"

log "A new member is announced"
# The org account lives in root:orgs, so its id starts with that cluster.
account_id=$(kcp "root:orgs:$ORG" get accountinfo account -o jsonpath='{.spec.organization.originClusterId}')/$ORG
# Start from a known state: the demo user is not a member yet.
add_member --remove "$DEMO_USER" member
sleep 10
before=$(count granted)
before_snapshots=$(snapshots)
add_member "$DEMO_USER" member
wait_for "membership.granted at the sink" 30 more_than granted "$before"
wait_for "a members snapshot at the sink" 30 more_snapshots_than "$before_snapshots"
event=$(last_event granted)
grep -q "source: platform-mesh.io/orgs/$ORG/iam" <<<"$event" && pass "source" || fail "source missing"
grep -q "subject: accounts/$account_id" <<<"$event" && pass "subject" || fail "subject missing"
grep -q "\"accountId\": \"$account_id\"" <<<"$event" && pass "accountId" || fail "accountId missing"
grep -q "pmorg: $ORG" <<<"$event" && pass "pmorg" || fail "pmorg missing"
grep -q '"role": "member"' <<<"$event" && pass "role" || fail "role missing"
grep -q "\"accountPath\": \"root:orgs:$ORG\"" <<<"$event" && pass "accountPath" || fail "accountPath missing"
grep -q "\"id\": \"$DEMO_USER\"" <<<"$event" && pass "user.id by the configured claim (email)" || fail "user missing"
# The demo user created the org, so they are its owner, too.
[[ $(roles "$account_id" "$DEMO_USER") == '["member","owner"]' ]] && pass "snapshot lists the new role" ||
  fail "snapshot: $(roles "$account_id" "$DEMO_USER")"
path=$(last_snapshot "$account_id" | jq -r .accountPath)
[[ $path == "root:orgs:$ORG" ]] && pass "snapshot accountPath" || fail "snapshot accountPath: $path"
# hack/steps/40-demo-org.sh gave the demo user a name in Keycloak.
names=$(last_snapshot "$account_id" | jq -r --arg u "$DEMO_USER" '.members[] | select(.user.id == $u) | "\(.user.firstName) \(.user.lastName)"')
[[ $names == "Eventing Demo" ]] && pass "snapshot has the member's names" || fail "snapshot names: $names"
email=$(last_snapshot "$account_id" | jq -r --arg u "$DEMO_USER" '.members[] | select(.user.id == $u) | .user.email')
[[ $email == "$DEMO_USER" ]] && pass "snapshot has the member's email" || fail "snapshot email: $email"

log "An invited user is announced the same way"
invited=invited-$RANDOM@example.com
before=$(count granted)
add_member "$invited" member
wait_for "membership.granted for the invited user" 30 more_than granted "$before"
grep -q "\"id\": \"$invited\"" <<<"$(last_event granted)" && pass "user.id=$invited" || fail "user id missing"
before=$(count revoked)
add_member --remove "$invited" member
wait_for "membership.revoked for the invited user" 30 more_than revoked "$before"

log "Removing the role is announced"
before=$(count revoked)
before_snapshots=$(snapshots)
add_member --remove "$DEMO_USER" member
wait_for "membership.revoked at the sink" 30 more_than revoked "$before"
grep -q "\"id\": \"$DEMO_USER\"" <<<"$(last_event revoked)" && pass "membership.revoked" || fail "revoked event wrong"
wait_for "a members snapshot at the sink" 30 more_snapshots_than "$before_snapshots"
[[ $(roles "$account_id" "$DEMO_USER") == '["owner"]' ]] && pass "snapshot without the removed role" ||
  fail "snapshot: $(roles "$account_id" "$DEMO_USER")"

log "A login is announced"
before=$(logins)
"$ROOT/hack/login.sh" >/dev/null
wait_for "user.loggedin at the sink" 60 more_logins_than "$before"
event=$(last_login)
grep -q "source: platform-mesh.io/orgs/$ORG/iam" <<<"$event" && pass "source" || fail "login source missing"
grep -q "subject: users/$DEMO_USER" <<<"$event" && pass "subject users/$DEMO_USER" || fail "login subject missing"
grep -q "pmorg: $ORG" <<<"$event" && pass "pmorg" || fail "login pmorg missing"
grep -q "\"id\": \"$DEMO_USER\"" <<<"$event" && pass "user.id by the configured claim (email)" || fail "login user missing"
grep -q "\"org\": \"$ORG\"" <<<"$event" && pass "org in the data" || fail "login org missing"
grep -q "\"email\": \"$DEMO_USER\"" <<<"$event" && pass "email from Keycloak" || fail "login email missing"
grep -q '"firstName": "Eventing"' <<<"$event" && grep -q '"lastName": "Demo"' <<<"$event" &&
  pass "names from Keycloak" || fail "login names missing"
grep -qi 'ipaddress\|sessionid' <<<"$event" && fail "login has an IP address or session id" || pass "no IP address or session id"
total=$(logins)
kind_kubectl -n "$NS" rollout restart deploy/eventing-keycloak-adapter >/dev/null
kind_kubectl -n "$NS" rollout status deploy/eventing-keycloak-adapter --timeout=120s >/dev/null
sleep 15
[[ $(logins) == "$total" ]] && pass "no duplicates after a restart" || fail "logins published again"

log "An invited user's first login sends their account's members"
invited=invited-$RANDOM@example.com
# member_names <user>: the user's names in the org account's last snapshot.
member_names() { last_snapshot "$account_id" | jq -r --arg u "$1" '.members[] | select(.user.id == $u) | "\(.user.firstName) \(.user.lastName)"'; }
listed() { [[ -n $(member_names "$1") ]]; }
add_member "$invited" member
wait_for "a snapshot with the invited user" 30 listed "$invited"
[[ $(member_names "$invited") == "null null" ]] && pass "no names before the first login" || fail "names: $(member_names "$invited")"
# Accepting an invite leaves the user in Keycloak with names and a password.
password=$(head -c 18 /dev/urandom | base64 | tr -d '/+=')
keycloak POST "/admin/realms/$ORG/users" -d "$(jq -nc --arg u "$invited" --arg p "$password" '{username: $u, email: $u,
  enabled: true, emailVerified: true, firstName: "Invited", lastName: "User", credentials: [{type: "password", value: $p, temporary: false}]}')" >/dev/null
"$ROOT/hack/login.sh" "$invited" "$password" >/dev/null
has_names() { [[ $(member_names "$1") == "Invited User" ]]; }
wait_for "a snapshot with the invited user's names" 60 has_names "$invited"
pass "snapshot after the first login has the names"
after=$(snapshots)
"$ROOT/hack/login.sh" "$invited" "$password" >/dev/null
sleep 15
[[ $(snapshots) == "$after" ]] && pass "a second login sends no snapshot" || fail "snapshots after the second login"
invited_id=$(keycloak GET "/admin/realms/$ORG/users?exact=true&username=$invited" | jq -r '.[0].id')
keycloak DELETE "/admin/realms/$ORG/users/$invited_id" >/dev/null
before=$(count revoked)
add_member --remove "$invited" member
wait_for "membership.revoked for the invited user" 30 more_than revoked "$before"
# shellcheck disable=SC2016 # expands in eval
wait_for "a snapshot without the invited user" 30 eval '! listed "$invited"'

log "Users joining and leaving the org are announced"
users() { sink_logs | grep -c "type: io.platform-mesh.iam.user.$1" || true; }
more_users_than() { (($(users "$1") > $2)); }
last_user_event() { sink_logs | grep -A23 "type: io.platform-mesh.iam.user.$1" | tail -24; }
# An invite writes the role for the email, before the user exists anywhere.
joiner=joiner-$RANDOM@example.com
before_created=$(users created)
before_deleted=$(users deleted)
before_revoked=$(count revoked)
add_member "$joiner" member
wait_for "user.created at the sink" 30 more_users_than created "$before_created"
event=$(last_user_event created)
grep -q "subject: users/$joiner" <<<"$event" && pass "user.created for the invited $joiner" || fail "user.created subject missing"
grep -q "\"email\": \"$joiner\"" <<<"$event" && pass "user.created has the email" || fail "user.created email missing"
# A second role and losing one of two say nothing about the org. A user event
# follows its change right away, so a few seconds after the revoke are enough.
add_member "$joiner" owner
add_member --remove "$joiner" member
wait_for "the revoke of one of two roles" 30 more_than revoked "$before_revoked"
sleep 5
[[ $(users created) == $((before_created + 1)) && $(users deleted) == "$before_deleted" ]] &&
  pass "nothing for a second role or losing one of two" || fail "user events for a second role"
add_member --remove "$joiner" owner
wait_for "user.deleted at the sink" 30 more_users_than deleted "$before_deleted"
event=$(last_user_event deleted)
grep -q "subject: users/$joiner" <<<"$event" && pass "user.deleted for $joiner" || fail "user.deleted subject missing"
grep -q '"email"\|"firstName"' <<<"$event" && fail "user.deleted has a profile" || pass "user.deleted has only the id"

log "The reader token has no admin"
reader_secret=$(kind_kubectl -n "$NS" get secret contrib-eventing-keycloak-reader -o jsonpath='{.data.client_secret}' | base64 -d)
reader_token=$(printf %s "$reader_secret" | curl -sS --fail-with-body --cacert "$SECRET_DIR/domain-ca.crt" \
  --resolve "$BASE_DOMAIN:$PORT:$INGRESS_ADDRESS" \
  "https://$BASE_DOMAIN:$PORT/keycloak/realms/master/protocol/openid-connect/token" \
  -d grant_type=client_credentials -d client_id=contrib-eventing-keycloak-reader \
  --data-urlencode client_secret@- | jq -r .access_token)
# JWT parts are unpadded base64url, and base64 -d cuts off an unpadded tail.
payload=$(cut -d. -f2 <<<"$reader_token" | tr '_-' '/+')
while ((${#payload} % 4)); do payload+="="; done
claims=$(base64 -d <<<"$payload" 2>/dev/null || true)
jq -e '(.realm_access.roles // []) | index("admin") == null and index("create-realm") == null' <<<"$claims" >/dev/null &&
  pass "no admin or create-realm role" || fail "reader token has realm roles: $(jq -c .realm_access <<<"$claims")"
# The account client's roles come with every service account's default roles; view-users brings query-users and query-groups with it.
jq -e '[(.resource_access // {}) | del(.account) | to_entries[] | select((.key | endswith("-realm") | not) or
    ((.value.roles // []) - ["view-events", "view-users", "query-groups", "query-users"] | length > 0))] | length == 0' <<<"$claims" >/dev/null &&
  pass "only view-events and view-users on the realms' management clients" ||
  fail "reader token roles: $(jq -c .resource_access <<<"$claims")"
code=$(curl -s -o /dev/null -w '%{http_code}' --cacert "$SECRET_DIR/domain-ca.crt" --resolve "$BASE_DOMAIN:$PORT:$INGRESS_ADDRESS" \
  -X PUT "https://$BASE_DOMAIN:$PORT/keycloak/admin/realms/$ORG/events/config" -H "Authorization: Bearer $reader_token" \
  -H 'Content-Type: application/json' -d '{}')
[[ $code == 403 ]] && pass "changing the event store: 403" || fail "changing the event store: $code"

log "Restarting the adapter does not publish again"
total=$(($(count granted) + $(count revoked) + $(snapshots)))
kind_kubectl -n "$NS" rollout restart deploy/eventing-openfga-adapter >/dev/null
kind_kubectl -n "$NS" rollout status deploy/eventing-openfga-adapter --timeout=120s >/dev/null
sleep 15
[[ $(($(count granted) + $(count revoked) + $(snapshots))) == "$total" ]] && pass "no duplicates" || fail "events published again"

log "A new org is covered from its first change"
neworg=e2e-org-$RANDOM
before=$(count granted)
kcp root:orgs apply --as "$DEMO_USER" -f - >/dev/null <<EOF
apiVersion: core.platform-mesh.io/v1alpha1
kind: Account
metadata:
  name: $neworg
spec:
  type: org
EOF
kcp root:orgs wait --for=condition=Ready "account/$neworg" --timeout=300s >/dev/null
wait_for "the owner grant of $neworg" 60 more_than granted "$before"
grep -q "pmorg: $neworg" <<<"$(last_event granted)" && pass "owner grant of $neworg" || fail "no event for $neworg"
kcp root:orgs delete account "$neworg" --wait=false >/dev/null

log "Accounts with the same name have different ids"
# A fresh name: the accounts of a previous run may still be terminating, since
# they are deleted with --wait=false.
acc=e2e-account-$RANDOM
seen=$(sink_logs | wc -l)
# Created as the demo user, who becomes owner; the owner grant is the event.
kcp "root:orgs:$ORG" apply --as "$DEMO_USER" -f - >/dev/null <<EOF
apiVersion: core.platform-mesh.io/v1alpha1
kind: Account
metadata:
  name: $acc
spec:
  type: account
EOF
kcp "root:orgs:$ORG" wait --for=condition=Ready "account/$acc" --timeout=180s >/dev/null
kcp "root:orgs:$ORG:$acc" apply --as "$DEMO_USER" -f - >/dev/null <<EOF
apiVersion: core.platform-mesh.io/v1alpha1
kind: Account
metadata:
  name: $acc
spec:
  type: account
EOF
kcp "root:orgs:$ORG:$acc" wait --for=condition=Ready "account/$acc" --timeout=180s >/dev/null
# Each account's owner grant names the workspace that holds the Account.
outer=$(kcp "root:orgs:$ORG" get accountinfo account -o jsonpath='{.spec.account.generatedClusterId}')/$acc
inner=$(kcp "root:orgs:$ORG:$acc" get accountinfo account -o jsonpath='{.spec.account.generatedClusterId}')/$acc
announced() { grep -q "subject: accounts/$1" <<<"$(sink_logs | tail -n +$((seen + 1)))"; }
wait_for "owner grant for the outer account" 30 announced "$outer" && pass "subject accounts/$outer"
wait_for "owner grant for the inner account" 30 announced "$inner" && pass "subject accounts/$inner"
owner_listed() { [[ $(roles "$inner" "$DEMO_USER") == '["owner"]' ]]; }
wait_for "a snapshot of the inner account" 30 owner_listed && pass "snapshot of the inner account"
path=$(last_snapshot "$inner" | jq -r .accountPath)
[[ $path == "root:orgs:$ORG:$acc:$acc" ]] && pass "accountPath $path" || fail "inner accountPath: $path"
kcp "root:orgs:$ORG:$acc" delete account "$acc" --wait=false >/dev/null
no_members() { [[ $(last_snapshot "$inner" | jq -c .members) == "[]" ]]; }
wait_for "an empty snapshot of the deleted account" 120 no_members && pass "deleted account: empty snapshot"
# Its AccountInfo is gone by now, but the path seen before stays.
path=$(last_snapshot "$inner" | jq -r .accountPath)
[[ $path == "root:orgs:$ORG:$acc:$acc" ]] && pass "deleted account keeps its accountPath" ||
  fail "deleted account accountPath: $path"
kcp "root:orgs:$ORG" delete account "$acc" --wait=false >/dev/null

log "A Subscription limited to other orgs gets nothing"
admin patch subscription $sub --type merge -p '{"spec":{"filter":{"orgs":["someone-else"]}}}' >/dev/null
wait_for "Subscription ready" 60 subscription_ready
sleep 5
before=$(count granted)
add_member "$DEMO_USER" member
sleep 15
[[ $(count granted) == "$before" ]] && pass "filtered out" || fail "event for another org delivered"
add_member --remove "$DEMO_USER" member
admin patch subscription $sub --type json -p '[{"op":"remove","path":"/spec/filter/orgs"}]' >/dev/null
wait_for "Subscription ready" 60 subscription_ready
sleep 15

log "Only known publishers get events into the broker"
probe() {
  kind_kubectl -n default run "e2e-probe-$RANDOM" --rm -i --restart=Never --quiet --image=curlimages/curl:8.5.0 --command -- \
    curl -s -m 5 -o /dev/null -w '%{http_code}' -X POST "$1" -H "Ce-Id: forged" -H "Ce-Specversion: 1.0" \
    -H "Ce-Type: io.platform-mesh.iam.membership.granted.v1alpha1" -H "Ce-Source: forged" -H "Ce-Pmorg: $ORG" \
    -H "Ce-Knativebrokerttl: 255" -H "Content-Type: application/json" ${2:+-H "Authorization: Bearer $2"} -d '{}' 2>/dev/null || true
}
code=$(probe "http://broker-ingress.knative-eventing.svc.cluster.local/$NS/default")
[[ $code != 202 ]] && pass "direct post to the broker is blocked" || fail "broker accepted a forged event"
# Behind the broker ingress: the channel, and the filter that delivers to a Trigger.
code=$(probe "$(kind_kubectl -n "$NS" get natsjetstreamchannel default-kne-trigger -o jsonpath='{.status.address.url}')")
[[ $code != 202 ]] && pass "direct post to the channel is blocked" || fail "channel accepted a forged event"
trigger=$(kind_kubectl -n "$NS" get triggers -o jsonpath='{.items[0].metadata.name}/{.items[0].metadata.uid}')
code=$(probe "http://broker-filter.knative-eventing.svc.cluster.local/triggers/$NS/$trigger")
[[ $code != 200 && $code != 202 ]] && pass "direct post to the broker filter is blocked" || fail "broker filter accepted a forged event"
# NATS has no auth; a client that gets in sees the server's INFO line.
# shellcheck disable=SC2016 # expands in the pod
nats=$(kind_kubectl -n default run "e2e-probe-$RANDOM" --rm -i --restart=Never --quiet --image=busybox:1.36 --command -- \
  sh -c 'echo "$(sleep 2 | nc -w 3 nats.nats-io.svc.cluster.local 4222)"' </dev/null 2>/dev/null || true)
[[ $nats != *INFO* ]] && pass "NATS is closed to other pods" || fail "another pod reached NATS"
code=$(probe "$INGRESS_URL")
[[ $code == 401 ]] && pass "ingress without token: 401" || fail "ingress without token: $code"
token=$(kind_kubectl -n default create token default --audience contrib-eventing)
code=$(probe "$INGRESS_URL" "$token")
[[ $code == 403 ]] && pass "ingress with another workload's token: 403" || fail "ingress with foreign token: $code"
host=eventing.services.$BASE_DOMAIN
# -k: the platform's certificate covers *.<domain> but not *.services.<domain>,
# so Traefik answers that listener with its own default certificate.
code=$(curl -sk -o /dev/null -w '%{http_code}' --resolve "$host:$PORT:$INGRESS_ADDRESS" \
  -X POST "https://$host:$PORT/" -H "Ce-Id: forged" -H "Ce-Specversion: 1.0" -H "Ce-Type: t" -H "Ce-Source: s" -d '{}')
[[ $code == 401 ]] && pass "Gateway route reaches the ingress (401 without token)" || fail "Gateway route: $code"

log "Replies from a sink don't reach other subscribers"
# A sink that answers every snapshot with a forged grant. Knative takes such an
# answer as a reply and sends it to the broker, which must not accept it. The
# sink doesn't get grants itself, so a reply that gets through can't loop.
# Knative counts the blocked reply as a failed delivery: the sink got the event
# once, and the event also goes to the dead letter sink.
forged=forged-reply-$RANDOM
kind_kubectl apply -f - >/dev/null <<YAML
apiVersion: v1
kind: ConfigMap
metadata: {name: reply-sink, namespace: eventing-demo-sink}
data:
  reply: |
    #!/bin/sh
    cat >/dev/null
    echo "\$HTTP_CE_ID" >>/tmp/got
    printf 'Content-Type: application/json\r\nCe-Id: $forged\r\nCe-Specversion: 1.0\r\nCe-Type: io.platform-mesh.iam.membership.granted.v1alpha1\r\nCe-Source: platform-mesh.io/orgs/$ORG/iam\r\nCe-Pmorg: $ORG\r\n\r\n{"org": "$ORG", "user": {"id": "$forged@example.com"}}'
---
apiVersion: v1
kind: Pod
metadata: {name: reply-sink, namespace: eventing-demo-sink, labels: {app: reply-sink}}
spec:
  containers:
    - name: httpd
      image: busybox:1.36
      command: [sh, -c, "mkdir -p /www/cgi-bin && cp /cgi/reply /www/cgi-bin/ && chmod +x /www/cgi-bin/reply && httpd -f -p 8080 -h /www"]
      volumeMounts: [{name: cgi, mountPath: /cgi}]
  volumes: [{name: cgi, configMap: {name: reply-sink}}]
---
apiVersion: v1
kind: Service
metadata: {name: reply-sink, namespace: eventing-demo-sink}
spec:
  selector: {app: reply-sink}
  ports: [{port: 80, targetPort: 8080}]
YAML
kind_kubectl -n eventing-demo-sink wait --for=condition=Ready pod/reply-sink --timeout=120s >/dev/null
admin apply -f - >/dev/null <<YAML
apiVersion: eventing.platform-mesh.io/v1alpha1
kind: Subscription
metadata: {name: e2e-replies}
spec:
  filter: {types: [io.platform-mesh.iam.account.members.v1alpha1]}
  sink: {uri: http://reply-sink.eventing-demo-sink.svc.cluster.local/cgi-bin/reply}
  delivery:
    retry: 1
    backoffDelay: PT0.2S
    deadLetterSink: {uri: http://event-display.eventing-demo-sink.svc.cluster.local}
YAML
reply_ready() { [[ $(admin get subscription e2e-replies -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}') == True ]]; }
wait_for "reply Subscription ready" 60 reply_ready
sleep 5
before=$(snapshots)
replied_before=$(sink_logs | grep -c "knativeerrordest: http://broker-ingress" || true)
add_member "$DEMO_USER" member
wait_for "the snapshot at event-display" 30 more_snapshots_than "$before"
sleep 20
[[ $(sink_logs | grep -c "id: $forged" || true) == 0 ]] && pass "forged reply blocked" || fail "a forged reply reached event-display"
got=$(kind_kubectl -n eventing-demo-sink exec reply-sink -- cat /tmp/got 2>/dev/null || true)
[[ $(wc -l <<<"$got") == 1 && -n $got ]] && pass "the replying sink got the snapshot once" || fail "replying sink got: $got"
# The blocked reply fails only when connecting times out, once per attempt.
replied() { sink_logs | grep -c "knativeerrordest: http://broker-ingress.knative-eventing.svc.cluster.local/$NS/default" || true; }
has_replied() { (($(replied) > replied_before)); }
wait_for "the snapshot at the dead letter sink" 240 has_replied
[[ $(replied) == $((replied_before + 1)) ]] && pass "the snapshot also went to the dead letter sink" ||
  fail "dead letters for the reply: $(($(replied) - replied_before))"
add_member --remove "$DEMO_USER" member
admin delete subscription e2e-replies --timeout=60s >/dev/null
kind_kubectl -n eventing-demo-sink delete pod/reply-sink svc/reply-sink cm/reply-sink --wait=false >/dev/null

log "Events a sink doesn't take go to the dead letter sink"
dead=http://nobody.eventing-demo-sink.svc.cluster.local
admin apply -f - >/dev/null <<YAML
apiVersion: eventing.platform-mesh.io/v1alpha1
kind: Subscription
metadata: {name: e2e-dead-letters}
spec:
  filter: {types: [io.platform-mesh.iam.account.members.v1alpha1]}
  sink: {uri: $dead}
  delivery:
    retry: 1
    backoffDelay: PT0.2S
    timeout: PT5S
    deadLetterSink: {uri: http://event-display.eventing-demo-sink.svc.cluster.local}
YAML
dead_ready() { [[ $(admin get subscription e2e-dead-letters -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}') == True ]]; }
wait_for "dead letter Subscription ready" 60 dead_ready
timeout=$(kind_kubectl -n "$NS" get triggers -o json | jq -r --arg u "$dead" '.items[] | select(.spec.subscriber.uri == $u) | .spec.delivery.timeout')
[[ $timeout == PT5S ]] && pass "timeout on the Trigger" || fail "Trigger timeout: $timeout"
sleep 5
# Knative marks dead letters with the destination that failed.
dead_letters() { sink_logs | grep -c "knativeerrordest: $dead" || true; }
before=$(dead_letters)
add_member "$DEMO_USER" member
more_dead_letters() { (($(dead_letters) > before)); }
wait_for "a dead letter at event-display" 60 more_dead_letters && pass "dead letter delivered"
add_member --remove "$DEMO_USER" member
admin delete subscription e2e-dead-letters --timeout=60s >/dev/null

log "Sinks outside the allowlist get nothing"
allowed=http://event-display.eventing-demo-sink.svc.cluster.local
not_allowed=http://example.com
reason() { admin get subscription e2e-not-allowed -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}'; }
is_reason() { [[ $(reason) == "$1" ]]; }
triggers() { kind_kubectl -n "$NS" get triggers -o json | jq '[.items[] | select(.metadata.annotations["eventing.platform-mesh.io/subscription"] == "e2e-not-allowed")] | length'; }
admin apply -f - >/dev/null <<YAML
apiVersion: eventing.platform-mesh.io/v1alpha1
kind: Subscription
metadata: {name: e2e-not-allowed}
spec:
  filter: {types: [io.platform-mesh.iam.account.members.v1alpha1]}
  sink: {uri: $not_allowed}
YAML
wait_for "SinkNotAllowed" 60 is_reason SinkNotAllowed && pass "sink $not_allowed: SinkNotAllowed"
[[ $(triggers) == 0 ]] && pass "no Trigger" || fail "Trigger for $not_allowed"
# An allowed sink works; a dead letter sink outside the allowlist stops it again.
admin patch subscription e2e-not-allowed --type merge -p "{\"spec\":{\"sink\":{\"uri\":\"$allowed\"}}}" >/dev/null
wait_for "Ready" 60 is_reason Ready && pass "allowed sink: Ready"
[[ $(triggers) == 1 ]] || fail "no Trigger for the allowed sink"
admin patch subscription e2e-not-allowed --type merge -p "{\"spec\":{\"delivery\":{\"deadLetterSink\":{\"uri\":\"$not_allowed\"}}}}" >/dev/null
wait_for "SinkNotAllowed" 60 is_reason SinkNotAllowed && pass "dead letter sink $not_allowed: SinkNotAllowed"
[[ $(triggers) == 0 ]] && pass "Trigger removed" || fail "Trigger still there"
admin delete subscription e2e-not-allowed --timeout=60s >/dev/null

log "The broker's channel retries, times out and redelivers as documented"
# probe answers by path: /status/<code>, /delay/<seconds>, anything else 200.
# It logs "got <path> <event id> <knativeerrordest or ->" for every request.
probe=http://probe.eventing-demo-sink.svc.cluster.local
kind_kubectl apply -f - >/dev/null <<'YAML'
apiVersion: v1
kind: ConfigMap
metadata: {name: probe, namespace: eventing-demo-sink}
data:
  probe.py: |
    import sys, threading, time
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
    lock = threading.Lock()
    class H(BaseHTTPRequestHandler):
        def log_message(self, *a): pass
        def do_POST(self):
            self.rfile.read(int(self.headers.get("content-length", 0)))
            with lock:
                sys.stdout.write(f"got {self.path} {self.headers.get('ce-id')} {self.headers.get('ce-knativeerrordest', '-')}\n")
                sys.stdout.flush()
            kind, _, arg = self.path.strip("/").partition("/")
            if kind == "delay":
                time.sleep(float(arg))
            try:
                self.send_response(int(arg) if kind == "status" else 200)
                self.send_header("content-length", "0")
                self.end_headers()
            except OSError:
                pass
    ThreadingHTTPServer(("", 8080), H).serve_forever()
---
apiVersion: v1
kind: Pod
metadata: {name: probe, namespace: eventing-demo-sink, labels: {app: probe}}
spec:
  containers:
    - name: probe
      image: python:3.12-alpine
      command: [python, -u, /probe/probe.py]
      volumeMounts: [{name: probe, mountPath: /probe}]
  volumes: [{name: probe, configMap: {name: probe}}]
---
apiVersion: v1
kind: Service
metadata: {name: probe, namespace: eventing-demo-sink}
spec:
  selector: {app: probe}
  ports: [{port: 80, targetPort: 8080}]
YAML
kind_kubectl -n eventing-demo-sink wait --for=condition=Ready pod/probe --timeout=180s >/dev/null
probe_logs() { kind_kubectl -n eventing-demo-sink logs probe; }
# tries <path> <event id>: how often the probe got the event on that path.
tries() { probe_logs | grep -c "^got $1 $2 " || true; }
# dest <path> <event id>: knativeerrordest of the event the probe got there.
dest() { probe_logs | grep "^got $1 $2 " | tail -1 | cut -d' ' -f4; }
# within <seconds> <command...>: like wait_for, but reports instead of exiting.
within() {
  local timeout=$1 start=$SECONDS
  shift
  until "$@" >/dev/null 2>&1; do
    ((SECONDS - start < timeout)) || return 1
    sleep 1
  done
}
at_least() { (($("${@:2}") >= $1)); }
probe_sub() { # probe_sub <name> <type> <path> <delivery as JSON>
  admin apply -f - >/dev/null <<YAML
apiVersion: eventing.platform-mesh.io/v1alpha1
kind: Subscription
metadata: {name: $1}
spec:
  filter: {types: [io.platform-mesh.iam.membership.$2.v1alpha1]}
  sink: {uri: $probe$3}
  delivery: $4
YAML
}
probe_ready() { [[ $(admin get subscription "$1" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}') == True ]]; }
dls() { echo "\"deadLetterSink\": {\"uri\": \"$probe/dls/$1\"}"; }
probe_sub e2e-404 granted /status/404 "{\"retry\": 2, \"backoffDelay\": \"PT0.2S\", $(dls 404)}"
probe_sub e2e-500 granted /status/500 "{\"retry\": 2, \"backoffDelay\": \"PT0.2S\", $(dls 500)}"
probe_sub e2e-slow granted /delay/10 "{\"retry\": 1, \"backoffDelay\": \"PT0.2S\", \"timeout\": \"PT2S\", $(dls slow)}"
probe_sub e2e-ok granted /ok '{"retry": 2}'
for s in e2e-404 e2e-500 e2e-slow e2e-ok; do wait_for "Subscription $s ready" 60 probe_ready $s; done
sleep 5
add_member "$DEMO_USER" member
got_ok() { probe_logs | grep -q "^got /ok "; }
wait_for "the event at the probe" 30 got_ok
id=$(probe_logs | grep "^got /ok " | tail -1 | cut -d' ' -f3)
start=$SECONDS
within 30 at_least 1 tries /dls/500 "$id" && [[ $(tries /status/500 "$id") == 3 && $(dest /dls/500 "$id") == "$probe/status/500" ]] &&
  pass "500: first try and 2 retries, then the dead letter sink" ||
  fail "500: $(tries /status/500 "$id") tries, $(tries /dls/500 "$id") dead letters from $(dest /dls/500 "$id")"
within 30 at_least 1 tries /dls/slow "$id" || true
slow=$((SECONDS - start))
# A timed-out request has no answer, so its dead letter has no knativeerror* attributes.
[[ $(tries /delay/10 "$id") == 2 && $(tries /dls/slow "$id") == 1 ]] && ((slow < 9)) &&
  pass "timeout: 2 tries cut off after 2s, dead letter after ${slow}s" ||
  fail "timeout: $(tries /delay/10 "$id") tries, $(tries /dls/slow "$id") dead letters after ${slow}s"
[[ $(tries /status/404 "$id") == 1 && $(tries /dls/404 "$id") == 0 ]] &&
  pass "404: one try, no dead letter" ||
  fail "404: $(tries /status/404 "$id") tries, $(tries /dls/404 "$id") dead letters"

# Knative recreates the channel subscription when the delivery changes. The new
# one must start at new events, not hand the probe the stream again.
channel_subs() { kind_kubectl -n "$NS" get subscriptions.messaging.knative.dev -o jsonpath='{.items[*].metadata.uid}' | tr ' ' '\n' | sort; }
before=$(channel_subs)
admin patch subscription e2e-ok --type merge -p '{"spec": {"delivery": {"retry": 3}}}' >/dev/null
recreated() { [[ $(channel_subs) != "$before" ]]; }
wait_for "the channel subscription to be recreated" 60 recreated
wait_for "Subscription e2e-ok ready" 60 probe_ready e2e-ok
sleep 15
[[ $(probe_logs | grep "^got /ok " | sort | uniq -d | wc -l) == 0 ]] &&
  pass "changed delivery: no event delivered again" ||
  fail "changed delivery replayed: $(probe_logs | grep "^got /ok " | sort | uniq -d | head -3)"
for s in e2e-404 e2e-500 e2e-slow e2e-ok; do admin delete subscription $s --timeout=60s >/dev/null; done

# The dispatcher dies while it delivers. JetStream hands the event out again.
probe_sub e2e-redeliver revoked /delay/8 '{"retry": 2, "timeout": "PT20S"}'
wait_for "Subscription e2e-redeliver ready" 60 probe_ready e2e-redeliver
sleep 5
add_member --remove "$DEMO_USER" member
got_slow() { probe_logs | grep -q "^got /delay/8 "; }
wait_for "the event at the probe" 30 got_slow
id=$(probe_logs | grep "^got /delay/8 " | tail -1 | cut -d' ' -f3)
kind_kubectl -n knative-eventing delete pod -l messaging.knative.dev/role=dispatcher --grace-period=0 --force >/dev/null 2>&1
within 120 at_least 2 tries /delay/8 "$id" && pass "redelivered after the dispatcher died" ||
  fail "not redelivered after the dispatcher died"
kind_kubectl -n knative-eventing rollout status deploy/jetstream-ch-dispatcher --timeout=120s >/dev/null
admin delete subscription e2e-redeliver --timeout=60s >/dev/null
kind_kubectl -n eventing-demo-sink delete pod/probe svc/probe cm/probe --wait=false >/dev/null

log "No events after the Subscription is deleted"
admin delete subscription $sub --timeout=60s >/dev/null
before=$(count granted)
add_member "$DEMO_USER" member
sleep 15
[[ $(count granted) == "$before" ]] && pass "nothing delivered" || fail "event delivered without Subscription"
add_member --remove "$DEMO_USER" member
admin apply -f "$ROOT/config/samples/subscription.yaml" >/dev/null
wait_for "Subscription ready" 120 subscription_ready

if ((failed)); then
  echo "e2e failed"
  exit 1
fi
echo "e2e passed"
