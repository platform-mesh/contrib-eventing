#!/usr/bin/env bash
# End-to-end check against an installation made by hack/install.sh. Leaves
# the installation as it found it.
source "$(dirname "$0")/../hack/lib.sh"
source "$ROOT/hack/steps/00-preflight.sh" >/dev/null

failed=0
pass() { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; failed=1; }

sub=memberships-to-event-display
admin() { kcp root:providers:eventing "$@"; }
sink_logs() { kind_kubectl -n eventing-demo-sink logs deploy/event-display; }
count() { sink_logs | grep -c "type: io.platform-mesh.iam.membership.$1" || true; }
more_than() { (($(count "$1") > $2)); }
# The last event of a type, as event-display prints it.
last_event() { sink_logs | grep -A23 "type: io.platform-mesh.iam.membership.$1" | tail -24; }
add_member() { "$ROOT/hack/add-member.sh" "$@" >/dev/null; }
subscription_ready() { [[ $(admin get subscription $sub -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}') == True ]]; }

log "The platform admin's Subscription is ready"
subscription_ready && pass "Ready in root:providers:eventing" || fail "Subscription not ready"

log "A new member is announced"
# The org account lives in root:orgs, so its id starts with that cluster.
account_id=$(kcp "root:orgs:$ORG" get accountinfo account -o jsonpath='{.spec.organization.originClusterId}')/$ORG
# Start from a known state: the demo user is not a member yet.
add_member --remove "$DEMO_USER" member
sleep 10
before=$(count granted)
add_member "$DEMO_USER" member
wait_for "membership.granted at the sink" 30 more_than granted "$before"
event=$(last_event granted)
grep -q "source: platform-mesh.io/orgs/$ORG/iam" <<<"$event" && pass "source" || fail "source missing"
grep -q "subject: accounts/$account_id" <<<"$event" && pass "subject" || fail "subject missing"
grep -q "\"accountId\": \"$account_id\"" <<<"$event" && pass "accountId" || fail "accountId missing"
grep -q "pmorg: $ORG" <<<"$event" && pass "pmorg" || fail "pmorg missing"
grep -q '"role": "member"' <<<"$event" && pass "role" || fail "role missing"
grep -q "\"id\": \"$DEMO_USER\"" <<<"$event" && pass "user.id by the configured claim (email)" || fail "user missing"

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
add_member --remove "$DEMO_USER" member
wait_for "membership.revoked at the sink" 30 more_than revoked "$before"
grep -q "\"id\": \"$DEMO_USER\"" <<<"$(last_event revoked)" && pass "membership.revoked" || fail "revoked event wrong"

log "Restarting the adapter does not publish again"
total=$(($(count granted) + $(count revoked)))
kind_kubectl -n "$NS" rollout restart deploy/openfga-adapter >/dev/null
kind_kubectl -n "$NS" rollout status deploy/openfga-adapter --timeout=120s >/dev/null
sleep 15
[[ $(($(count granted) + $(count revoked))) == "$total" ]] && pass "no duplicates" || fail "events published again"

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
# A fresh name: Platform Mesh keeps the tuples of deleted accounts, so an owner
# grant for a name used before is no change and no event.
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
kcp "root:orgs:$ORG:$acc" delete account "$acc" --wait=false >/dev/null
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
    -H "Content-Type: application/json" ${2:+-H "Authorization: Bearer $2"} -d '{}' 2>/dev/null || true
}
code=$(probe "http://broker-ingress.knative-eventing.svc.cluster.local/$NS/default")
[[ $code != 202 ]] && pass "direct post to the broker is blocked" || fail "broker accepted a forged event"
code=$(probe http://ingress.contrib-eventing.svc.cluster.local)
[[ $code == 401 ]] && pass "ingress without token: 401" || fail "ingress without token: $code"
token=$(kind_kubectl -n default create token default --audience contrib-eventing)
code=$(probe http://ingress.contrib-eventing.svc.cluster.local "$token")
[[ $code == 403 ]] && pass "ingress with another workload's token: 403" || fail "ingress with foreign token: $code"
host=eventing.services.$BASE_DOMAIN
# -k: the platform's certificate covers *.<domain> but not *.services.<domain>,
# so Traefik answers that listener with its own default certificate.
code=$(curl -sk -o /dev/null -w '%{http_code}' --resolve "$host:$PORT:$INGRESS_ADDRESS" \
  -X POST "https://$host:$PORT/" -H "Ce-Id: forged" -H "Ce-Specversion: 1.0" -H "Ce-Type: t" -H "Ce-Source: s" -d '{}')
[[ $code == 401 ]] && pass "Gateway route reaches the ingress (401 without token)" || fail "Gateway route: $code"

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
