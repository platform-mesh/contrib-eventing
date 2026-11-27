/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-mesh/contrib-eventing/internal/contract"
	"github.com/platform-mesh/contrib-eventing/internal/keycloak"
	"go.platform-mesh.io/golang-commons/logger/testlogger"
)

// fakeKeycloakUsers serves the token endpoint and the users of realm acme:
// alice has names, bob was invited and has none. With down set it answers 500.
func fakeKeycloakUsers(t *testing.T, down *atomic.Bool) *keycloak.Users {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/master/protocol/openid-connect/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"access_token":"tok","token_type":"Bearer","expires_in":300}`)
			return
		}
		if down.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		switch {
		case r.URL.Path == "/admin/realms/acme/users" && r.URL.Query().Get("username") == "alice@example.com":
			_, _ = fmt.Fprint(w, `[{"id":"k-alice","username":"alice@example.com","email":"alice@example.com","firstName":"Alice","lastName":"Liddell"}]`)
		case r.URL.Path == "/admin/realms/acme/users" && r.URL.Query().Get("username") == "bob@example.com":
			_, _ = fmt.Fprint(w, `[{"id":"k-bob","username":"bob@example.com","email":"bob@example.com"}]`)
		case r.URL.Path == "/admin/realms/acme/users/k-alice":
			_, _ = fmt.Fprint(w, `{"id":"k-alice","username":"alice@example.com","email":"alice@example.com","firstName":"Alice","lastName":"Liddell"}`)
		case r.URL.Path == "/admin/realms/acme/users":
			_, _ = fmt.Fprint(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &keycloak.Users{Client: keycloak.NewClient(context.Background(), srv.URL, "reader", "s3cret")}
}

func TestKeycloakProfiles(t *testing.T) {
	var down atomic.Bool
	users := fakeKeycloakUsers(t, &down)
	ctx := context.Background()

	byEmail := &KeycloakProfiles{Users: users, UserClaim: "email"}
	p, err := byEmail.Profile(ctx, "acme", "alice@example.com")
	require.NoError(t, err)
	assert.Equal(t, Profile{Email: "alice@example.com", FirstName: "Alice", LastName: "Liddell"}, p)

	p, err = byEmail.Profile(ctx, "acme", "bob@example.com")
	require.NoError(t, err)
	assert.Equal(t, Profile{Email: "bob@example.com"}, p, "an invited user has no names")

	p, err = byEmail.Profile(ctx, "acme", "nobody@example.com")
	require.NoError(t, err)
	assert.Empty(t, p, "an unknown user has nothing")

	bySub := &KeycloakProfiles{Users: users, UserClaim: "sub"}
	p, err = bySub.Profile(ctx, "acme", "k-alice")
	require.NoError(t, err)
	assert.Equal(t, Profile{Email: "alice@example.com", FirstName: "Alice", LastName: "Liddell"}, p)
}

func TestSnapshotProfiles(t *testing.T) {
	// Alice and bob are members of c1/team-a in the shared fake tuples.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/read") {
			serveRead(t, w, r)
			return
		}
		if r.URL.Query().Get("continuation_token") == "after-page" {
			_, _ = w.Write([]byte(`{"changes": [], "continuation_token": "after-page"}`))
			return
		}
		_, _ = w.Write([]byte(threeChanges))
	}))
	defer srv.Close()

	var down atomic.Bool
	var published []cloudevents.Event
	a := &Adapter{
		FGA:       &Client{BaseURL: srv.URL, HTTP: srv.Client()},
		Log:       testlogger.New().HideLogOutput().Logger,
		UserClaim: "email",
		Profiles:  &KeycloakProfiles{Users: fakeKeycloakUsers(t, &down), UserClaim: "email"},
		Publish: func(_ context.Context, e cloudevents.Event) error {
			published = append(published, e)
			return nil
		},
	}
	snapshot := func(t *testing.T) contract.AccountMembers {
		published = nil
		st := orgState{StoreID: "store-1", Since: time.Now()}
		// "acme" is the realm the fake Keycloak knows.
		_, err := a.publishNew(context.Background(), "acme", &st)
		require.NoError(t, err)
		for _, e := range published {
			if e.Type() == contract.TypeAccountMembers && e.Subject() == "accounts/c1/team-a" {
				var m contract.AccountMembers
				require.NoError(t, e.DataAs(&m))
				return m
			}
		}
		t.Fatal("no snapshot")
		return contract.AccountMembers{}
	}

	m := snapshot(t)
	assert.Empty(t, m.Errors, "nothing went wrong")
	require.Len(t, m.Members, 2)
	assert.Equal(t, contract.User{ID: "alice@example.com", Email: "alice@example.com", FirstName: "Alice", LastName: "Liddell"}, m.Members[0].User)
	assert.Equal(t, contract.User{ID: "bob@example.com", Email: "bob@example.com"}, m.Members[1].User, "an invite has no names")

	// The changes themselves never carry a profile.
	for _, e := range published {
		if e.Type() == contract.TypeMembershipGranted {
			assert.NotContains(t, string(e.Data()), "firstName")
			assert.NotContains(t, string(e.Data()), "email")
		}
	}

	// Keycloak down: the snapshot still goes out, without names. The email is
	// the user id under this claim and stays.
	down.Store(true)
	a.Profiles = &KeycloakProfiles{Users: fakeKeycloakUsers(t, &down), UserClaim: "email"}
	m = snapshot(t)
	require.Len(t, m.Members, 2)
	assert.Equal(t, contract.User{ID: "alice@example.com", Email: "alice@example.com"}, m.Members[0].User)
	assert.Equal(t, []contract.SnapshotError{contract.ErrorNamesUnavailable}, m.Errors, "the snapshot says why it has no names")

	// No Keycloak: no name fields and no errors, but the emails.
	a.Profiles = nil
	m = snapshot(t)
	assert.Equal(t, contract.User{ID: "alice@example.com", Email: "alice@example.com"}, m.Members[0].User)
	for _, e := range published {
		assert.NotContains(t, string(e.Data()), "firstName")
		assert.NotContains(t, string(e.Data()), "lastName")
		assert.NotContains(t, string(e.Data()), "errors")
	}
}

// Under the sub claim the user id is the Keycloak id, so the email comes from
// Keycloak with the names, or not at all.
func TestEmailUnderSub(t *testing.T) {
	members := func() []contract.Member {
		return []contract.Member{{User: contract.User{ID: "k-alice"}}, {User: contract.User{ID: "invited@example.com"}}}
	}
	var down atomic.Bool
	a := &Adapter{Log: testlogger.New().HideLogOutput().Logger, UserClaim: "sub",
		Profiles: &KeycloakProfiles{Users: fakeKeycloakUsers(t, &down), UserClaim: "sub"}}

	m := members()
	assert.True(t, a.addProfiles(context.Background(), "acme", m))
	assert.Equal(t, contract.User{ID: "k-alice", Email: "alice@example.com", FirstName: "Alice", LastName: "Liddell"}, m[0].User)
	assert.Equal(t, contract.User{ID: "invited@example.com"}, m[1].User, "an invite stored under its email is not found by Keycloak id")

	down.Store(true)
	m = members()
	assert.False(t, a.addProfiles(context.Background(), "acme", m))
	assert.Equal(t, members(), m, "no email without Keycloak")

	a.Profiles = nil
	m = members()
	assert.True(t, a.addProfiles(context.Background(), "acme", m))
	assert.Equal(t, members(), m)
}

// flakyProfiles knows alice, fails for bob, or never answers.
type flakyProfiles struct{ hang bool }

func (f flakyProfiles) Profile(ctx context.Context, _, userID string) (Profile, error) {
	if f.hang {
		<-ctx.Done()
		return Profile{}, ctx.Err()
	}
	if userID == "k-alice" {
		return Profile{Email: "alice@example.com", FirstName: "Alice", LastName: "Liddell"}, nil
	}
	return Profile{}, errors.New("keycloak failed")
}

func TestProfilesAllOrNothing(t *testing.T) {
	members := func() []contract.Member {
		return []contract.Member{{User: contract.User{ID: "k-alice"}}, {User: contract.User{ID: "k-bob"}}}
	}
	a := &Adapter{Log: testlogger.New().HideLogOutput().Logger, UserClaim: "sub", Profiles: flakyProfiles{}}

	// Alice is read, bob fails: nobody gets a name or an email.
	m := members()
	assert.False(t, a.addProfiles(context.Background(), "acme", m))
	assert.Equal(t, members(), m)

	// Keycloak hangs: the one deadline ends the profile step and the snapshot goes out.
	a = &Adapter{Log: testlogger.New().HideLogOutput().Logger, UserClaim: "sub", Profiles: flakyProfiles{hang: true}, ProfileTimeout: 50 * time.Millisecond}
	m = members()
	start := time.Now()
	assert.False(t, a.addProfiles(context.Background(), "acme", m))
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Equal(t, members(), m)
}
