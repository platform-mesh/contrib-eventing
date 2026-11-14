#!/usr/bin/env bash
# Logs the demo user in to the demo organization with the password grant. For
# Keycloak this is a normal login, so it records a LOGIN event.
source "$(dirname "$0")/lib.sh"
source "$SECRET_DIR/demo-user.env"

token=$(curl -sS --fail-with-body --cacert "$SECRET_DIR/domain-ca.crt" \
  --resolve "$BASE_DOMAIN:$PORT:$INGRESS_ADDRESS" \
  "https://$BASE_DOMAIN:$PORT/keycloak/realms/$ORG/protocol/openid-connect/token" \
  -d grant_type=password -d client_id=contrib-eventing-login-test -d scope=openid \
  --data-urlencode "username=$DEMO_USER" --data-urlencode "password=$DEMO_PASSWORD" | jq -r .access_token)

# The token's email claim, or sub, is the user id the login event carries,
# depending on the platform's user claim.
claims=$(cut -d. -f2 <<<"$token" | tr '_-' '/+' | base64 -d 2>/dev/null || true)
echo "Logged in to $ORG as $DEMO_USER (email $(jq -r .email <<<"$claims"), sub $(jq -r .sub <<<"$claims"))"
