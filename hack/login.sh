#!/usr/bin/env bash
# Logs a user in to the demo organization with the password grant. For
# Keycloak this is a normal login, so it records a LOGIN event.
#   ./hack/login.sh [user password]
# Default: the demo user.
source "$(dirname "$0")/lib.sh"

if (($# == 2)); then
  user=$1 password=$2
elif (($# == 0)); then
  source "$SECRET_DIR/demo-user.env"
  user=$DEMO_USER password=$DEMO_PASSWORD
else
  die "usage: $0 [user password]"
fi

token=$(curl -sS --fail-with-body --cacert "$SECRET_DIR/domain-ca.crt" \
  --resolve "$BASE_DOMAIN:$PORT:$INGRESS_ADDRESS" \
  "https://$BASE_DOMAIN:$PORT/keycloak/realms/$ORG/protocol/openid-connect/token" \
  -d grant_type=password -d client_id=contrib-eventing-login-test -d scope=openid \
  --data-urlencode "username=$user" --data-urlencode "password=$password" | jq -r .access_token)

# The token's email claim, or sub, is the user id the login event carries,
# depending on the platform's user claim.
claims=$(cut -d. -f2 <<<"$token" | tr '_-' '/+' | base64 -d 2>/dev/null || true)
echo "Logged in to $ORG as $user (email $(jq -r .email <<<"$claims"), sub $(jq -r .sub <<<"$claims"))"
