/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package keycloak

import (
	"context"
	"sync"
	"time"
)

// DefaultNameTTL is how long a found user with names is remembered.
const DefaultNameTTL = 5 * time.Minute

// Users caches user lookups. Only users that were found and have a name are
// cached. Misses and users without names are rare (invited users who haven't
// set a name yet), and caching them would hide a name that is set later.
type Users struct {
	Client *Client
	// TTL defaults to DefaultNameTTL.
	TTL time.Duration

	// now can be replaced in tests.
	now func() time.Time

	mu    sync.Mutex
	cache map[string]cachedUser
}

type cachedUser struct {
	user    User
	expires time.Time
}

// ByID looks a user up by Keycloak id.
func (u *Users) ByID(ctx context.Context, realm, id string) (User, bool, error) {
	return u.lookup("id/"+realm+"/"+id, func() (User, bool, error) { return u.Client.User(ctx, realm, id) })
}

// ByUsername looks a user up by exact username.
func (u *Users) ByUsername(ctx context.Context, realm, username string) (User, bool, error) {
	return u.lookup("name/"+realm+"/"+username, func() (User, bool, error) {
		return u.Client.UserByUsername(ctx, realm, username)
	})
}

func (u *Users) lookup(key string, fetch func() (User, bool, error)) (User, bool, error) {
	u.mu.Lock()
	c, ok := u.cache[key]
	u.mu.Unlock()
	if ok && u.clock().Before(c.expires) {
		return c.user, true, nil
	}
	user, found, err := fetch()
	if err != nil || !found {
		return User{}, false, err
	}
	if user.FirstName != "" || user.LastName != "" {
		ttl := u.TTL
		if ttl == 0 {
			ttl = DefaultNameTTL
		}
		u.mu.Lock()
		if u.cache == nil {
			u.cache = map[string]cachedUser{}
		}
		u.cache[key] = cachedUser{user: user, expires: u.clock().Add(ttl)}
		u.mu.Unlock()
	}
	return user, true, nil
}

func (u *Users) clock() time.Time {
	if u.now != nil {
		return u.now()
	}
	return time.Now()
}
