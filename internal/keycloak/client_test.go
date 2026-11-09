/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package keycloak

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fake serves the token endpoint and calls h for everything else.
func fake(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/master/protocol/openid-connect/token" {
			require.NoError(t, r.ParseForm())
			assert.Equal(t, "client_credentials", r.Form.Get("grant_type"))
			user, pass, _ := r.BasicAuth()
			assert.Equal(t, "reader", user)
			assert.Equal(t, "s3cret", pass)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"access_token":"tok","token_type":"Bearer","expires_in":300}`)
			return
		}
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return NewClient(context.Background(), srv.URL, "reader", "s3cret")
}

func TestRealms(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/admin/realms", r.URL.Path)
		_, _ = fmt.Fprint(w, `[{"id":"i1","realm":"acme"},{"id":"i2","realm":"master"}]`)
	})
	realms, err := c.Realms(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []Realm{{ID: "i1", Realm: "acme"}, {ID: "i2", Realm: "master"}}, realms)
}

func TestEvents(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/admin/realms/acme/events", r.URL.Path)
		q := r.URL.Query()
		assert.Equal(t, "LOGIN", q.Get("type"))
		assert.Equal(t, "1700", q.Get("dateFrom"))
		assert.Equal(t, "asc", q.Get("direction"))
		assert.Equal(t, "7", q.Get("max"))
		// Out of order on purpose.
		_, _ = fmt.Fprint(w, `[{"id":"e2","time":1702,"type":"LOGIN","userId":"u1","details":{"username":"a@example.com"}},
			{"id":"e1","time":1701,"type":"LOGIN","userId":"u1"}]`)
	})
	events, err := c.Events(context.Background(), "acme", "LOGIN", 1700, 7)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "e1", events[0].ID, "sorted by time")
	assert.Equal(t, "a@example.com", events[1].Details["username"])
}

func TestUsers(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/admin/realms/acme/users/u1":
			_, _ = fmt.Fprint(w, `{"id":"u1","username":"a@example.com","firstName":"Ada","lastName":"Lovelace"}`)
		case r.URL.Path == "/admin/realms/acme/users" && r.URL.Query().Get("username") == "a@example.com":
			assert.Equal(t, "true", r.URL.Query().Get("exact"))
			_, _ = fmt.Fprint(w, `[{"id":"u1","username":"a@example.com","firstName":"Ada"}]`)
		case r.URL.Path == "/admin/realms/acme/users":
			_, _ = fmt.Fprint(w, `[]`)
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	})
	ctx := context.Background()

	u, found, err := c.User(ctx, "acme", "u1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "Lovelace", u.LastName)

	_, found, err = c.User(ctx, "acme", "gone")
	require.NoError(t, err, "404 is not an error")
	assert.False(t, found)

	u, found, err = c.UserByUsername(ctx, "acme", "a@example.com")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "u1", u.ID)

	_, found, err = c.UserByUsername(ctx, "acme", "nobody@example.com")
	require.NoError(t, err, "an empty list is not an error")
	assert.False(t, found)
}

func TestErrorBody(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, strings.Repeat("x", 1000), http.StatusForbidden)
	})
	_, err := c.Realms(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Less(t, len(err.Error()), 400, "the body is cut")

	_, _, err = c.User(context.Background(), "acme", "u1")
	assert.Error(t, err, "only 404 means not found")
}

func TestUsersCache(t *testing.T) {
	calls := 0
	names := map[string]string{"named": "Ada", "invited": ""}
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		first, ok := names[id]
		if !ok {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"username":"%s@example.com","firstName":%q}`, id, id, first)
	})
	now := time.Unix(1000, 0)
	users := &Users{Client: c, TTL: time.Minute, now: func() time.Time { return now }}
	ctx := context.Background()

	for range 3 {
		u, found, err := users.ByID(ctx, "acme", "named")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "Ada", u.FirstName)
	}
	assert.Equal(t, 1, calls, "a user with a name is cached")

	now = now.Add(2 * time.Minute)
	_, _, _ = users.ByID(ctx, "acme", "named")
	assert.Equal(t, 2, calls, "until the TTL is over")

	calls = 0
	for range 2 {
		u, found, _ := users.ByID(ctx, "acme", "invited")
		assert.True(t, found)
		assert.Empty(t, u.FirstName)
	}
	assert.Equal(t, 2, calls, "a user without a name is looked up again")

	calls = 0
	for range 2 {
		_, found, err := users.ByID(ctx, "acme", "gone")
		require.NoError(t, err)
		assert.False(t, found)
	}
	assert.Equal(t, 2, calls, "a miss is not cached")
}
