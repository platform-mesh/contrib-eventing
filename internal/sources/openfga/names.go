/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"context"

	"github.com/platform-mesh/contrib-eventing/internal/keycloak"
)

// NameLookup finds the first and last name of a user of an org. Unknown users
// and users without names give empty strings and no error.
type NameLookup interface {
	Names(ctx context.Context, org, userID string) (first, last string, err error)
}

// KeycloakNames reads names from the org's Keycloak realm. The user id is
// what the platform's user claim has: the username for email, the Keycloak id
// for sub.
type KeycloakNames struct {
	Users *keycloak.Users
	// UserClaim is email or sub.
	UserClaim string
}

func (k *KeycloakNames) Names(ctx context.Context, org, userID string) (string, string, error) {
	var (
		u     keycloak.User
		found bool
		err   error
	)
	if k.UserClaim == "sub" {
		u, found, err = k.Users.ByID(ctx, org, userID)
	} else {
		u, found, err = k.Users.ByUsername(ctx, org, userID)
	}
	if err != nil || !found {
		return "", "", err
	}
	return u.FirstName, u.LastName, nil
}
