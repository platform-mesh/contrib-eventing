# Creates a demo organization, so there are memberships to watch, and lets its
# user log in, so there are logins to watch.
#
# This follows installation/organization-onboarding.md in helm-charts, with one
# shortcut: instead of logging in as the new user, the kcp admin creates the
# Account while impersonating them. The Account webhook then records the user
# as creator, and Platform Mesh invites them into the organization's realm.

log "Creating demo organization $ORG for $DEMO_USER"
kcp root:orgs apply --as "$DEMO_USER" -f - >/dev/null <<EOF
apiVersion: core.platform-mesh.io/v1alpha1
kind: Account
metadata:
  name: $ORG
spec:
  type: org
  displayName: Eventing demo
EOF
kcp root:orgs wait --for=condition=Ready "account/$ORG" --timeout=300s >/dev/null

demo_user_id() {
  keycloak GET "/admin/realms/$ORG/users?exact=true&username=$DEMO_USER" | jq -er '.[0].id'
}
# One call in this shell fetches the admin token; wait_for runs in this shell,
# command substitutions below don't, and would log in again each time.
keycloak GET /admin/realms >/dev/null
wait_for "user $DEMO_USER in realm $ORG" 180 demo_user_id
user_id=$(demo_user_id)

# The invite leaves the user with required actions (set password, verify
# email) and without the first and last name the realm's user profile
# requires. Fill that in and set a password, so the demo can log in directly.
log "Letting $DEMO_USER log in to $ORG"
if [[ ! -f $SECRET_DIR/demo-user.env ]]; then
  echo "DEMO_PASSWORD=$(head -c 18 /dev/urandom | base64 | tr -d '/+=')" >"$SECRET_DIR/demo-user.env"
  chmod 600 "$SECRET_DIR/demo-user.env"
fi
source "$SECRET_DIR/demo-user.env"
keycloak PUT "/admin/realms/$ORG/users/$user_id" \
  -d '{"requiredActions": [], "emailVerified": true, "firstName": "Eventing", "lastName": "Demo"}' >/dev/null
keycloak PUT "/admin/realms/$ORG/users/$user_id/reset-password" \
  -d "$(jq -nc --arg p "$DEMO_PASSWORD" '{type: "password", value: $p, temporary: false}')" >/dev/null

# A public client that allows only the password grant, used by hack/login.sh.
# The portal's own client stays untouched.
if [[ $(keycloak GET "/admin/realms/$ORG/clients?clientId=contrib-eventing-login-test" | jq length) == 0 ]]; then
  keycloak POST "/admin/realms/$ORG/clients" -d '{
    "clientId": "contrib-eventing-login-test", "publicClient": true,
    "standardFlowEnabled": false, "implicitFlowEnabled": false, "directAccessGrantsEnabled": true
  }' >/dev/null
fi
