/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"context"

	"github.com/platform-mesh/contrib-eventing/internal/keycloak"
)

// Profile is what the identity provider knows about a user besides the id.
type Profile struct {
	Email     string
	FirstName string
	LastName  string
}

// ProfileLookup finds the profile of a user of an org. Unknown users give an
// empty profile and no error.
type ProfileLookup interface {
	Profile(ctx context.Context, org, userID string) (Profile, error)
}

// KeycloakProfiles reads profiles from the org's Keycloak realm. The user id
// is what the platform's user claim has: the username for email, the Keycloak
// id for sub.
type KeycloakProfiles struct {
	Users *keycloak.Users
	// UserClaim is email or sub.
	UserClaim string
}

func (k *KeycloakProfiles) Profile(ctx context.Context, org, userID string) (Profile, error) {
	var (
		u     keycloak.User
		found bool
		err   error
	)
	if k.UserClaim == claimSub {
		u, found, err = k.Users.ByID(ctx, org, userID)
	} else {
		u, found, err = k.Users.ByUsername(ctx, org, userID)
	}
	if err != nil || !found {
		return Profile{}, err
	}
	return Profile{Email: u.Email, FirstName: u.FirstName, LastName: u.LastName}, nil
}
