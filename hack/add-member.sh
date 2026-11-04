#!/usr/bin/env bash
# Gives a user a role on the demo org, or takes it away with --remove.
#   ./hack/add-member.sh [--remove] [email] [role]
# Defaults: the demo user and the member role.
#
# It changes OpenFGA the way iam-service does when you add or remove a member
# in the portal, but directly. The adapter reads every change from the
# changelog, so it doesn't matter who made it.
source "$(dirname "$0")/lib.sh"

remove=false
if [[ ${1:-} == --remove ]]; then
  remove=true
  shift
fi
email=${1:-$DEMO_USER}
role=${2:-member}

# The account's cluster id is part of the OpenFGA object ids.
cluster=$(kcp "root:orgs:$ORG" get accountinfo account -o jsonpath='{.spec.organization.originClusterId}')
account="core_platform-mesh_io_account:$cluster/$ORG"
role_object="role:core_platform-mesh_io_account/$cluster/$ORG/$role"

# Let kubectl pick a free local port and read it from its output.
pf_log=$(mktemp)
# kubectl directly, not the kind_kubectl function, so $! is the kubectl process.
kubectl --context "$KUBE_CONTEXT" -n platform-mesh-system port-forward svc/openfga :8080 >"$pf_log" &
pf=$!
trap 'kill $pf; wait $pf 2>/dev/null || true; rm -f "$pf_log"' EXIT
fga_port() { sed -n 's/^Forwarding from 127.0.0.1:\([0-9]*\).*/\1/p' "$pf_log" | grep .; }
wait_for "OpenFGA port-forward" 15 fga_port
fga=localhost:$(fga_port)

store=$(curl -sS "$fga/stores?page_size=100" | jq -er --arg org "$ORG" '.stores[] | select(.name == $org) | .id')
fga_write() { curl -sS --fail-with-body "$fga/stores/$store/write" -d "$1" >/dev/null; }
assignee=$(jq -nc --arg user "user:$email" --arg ro "$role_object" '{user: $user, relation: "assignee", object: $ro}')

if $remove; then
  # Only the user's assignment goes; the role itself is shared with other members.
  fga_write "{\"deletes\": {\"tuple_keys\": [$assignee], \"on_missing\": \"ignore\"}}"
  echo "$email is no longer $role of $ORG"
else
  role_on_account=$(jq -nc --arg ro "$role_object#assignee" --arg role "$role" --arg acc "$account" \
    '{user: $ro, relation: $role, object: $acc}')
  fga_write "{\"writes\": {\"tuple_keys\": [$assignee, $role_on_account], \"on_duplicate\": \"ignore\"}}"
  echo "$email is now $role of $ORG"
fi
