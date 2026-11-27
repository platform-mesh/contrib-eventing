/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"context"
	"fmt"
	"time"

	"github.com/platform-mesh/contrib-eventing/internal/keycloak"
)

const loginType = "LOGIN"

// FirstLogin is a user's first login to an org.
type FirstLogin struct {
	// ID identifies the login, for event ids.
	ID string
	// UserID is the user as OpenFGA has them, by the user claim.
	UserID string
	At     time.Time
}

// KeycloakLogins finds first logins in the org realms' event stores. A login
// is first if the store has no earlier login of the user. The store keeps
// logins for a limited time, so the first login after a longer pause counts as
// first, too.
type KeycloakLogins struct {
	KC *keycloak.Client
	// UserClaim is email or sub.
	UserClaim string
}

// EachNew hands the org's new first logins to handle, oldest first, and moves
// cur past the logins it read.
func (k *KeycloakLogins) EachNew(ctx context.Context, org string, cur *keycloak.Cursor, handle func(FirstLogin) error) (bool, error) {
	return k.KC.EachNew(ctx, org, loginType, cur, func(ev keycloak.Event) error {
		if ev.UserID == "" {
			return nil
		}
		before, err := k.KC.EventBefore(ctx, org, loginType, ev.UserID, ev.Time)
		if err != nil || before {
			return err
		}
		id := ev.UserID
		if k.UserClaim != claimSub {
			u, found, err := k.KC.User(ctx, org, ev.UserID)
			if err != nil {
				return fmt.Errorf("reading user %s of %s: %w", ev.UserID, org, err)
			}
			username := ev.Details["username"]
			if found {
				username = u.Username
			}
			id = keycloak.UserID(k.UserClaim, ev.UserID, username)
		}
		if id == "" {
			return nil
		}
		return handle(FirstLogin{ID: ev.ID, UserID: id, At: time.UnixMilli(ev.Time)})
	})
}
