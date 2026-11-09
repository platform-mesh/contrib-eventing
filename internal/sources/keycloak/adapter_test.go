/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package keycloak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-mesh/contrib-eventing/internal/contract"
	kc "github.com/platform-mesh/contrib-eventing/internal/keycloak"
	"go.platform-mesh.io/golang-commons/logger/testlogger"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// fakeKeycloak serves what the adapter reads. Like Keycloak it checks the
// client credentials and the bearer token, and includes events at exactly
// dateFrom.
type fakeKeycloak struct {
	t  *testing.T
	mu sync.Mutex
	// realms: name -> id
	realms map[string]string
	events map[string][]kc.Event
	// users by id, the same in every realm
	users map[string]kc.User
	// dateFroms records the dateFrom of every events request per realm.
	dateFroms map[string][]int64
}

func newFakeKeycloak(t *testing.T) *fakeKeycloak {
	return &fakeKeycloak{
		t:         t,
		realms:    map[string]string{"master": "id-master", "welcome": "id-welcome"},
		events:    map[string][]kc.Event{},
		users:     map[string]kc.User{},
		dateFroms: map[string][]int64{},
	}
}

func (f *fakeKeycloak) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/realms/master/protocol/openid-connect/token" {
		user, pass, _ := r.BasicAuth()
		if user != "reader" || pass != "s3cret" {
			http.Error(w, "bad client", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"tok","token_type":"Bearer","expires_in":300}`)
		return
	}
	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, "no token", http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // admin realms {realm} ...
	switch {
	case r.URL.Path == "/admin/realms":
		var list []kc.Realm
		for name, id := range f.realms {
			list = append(list, kc.Realm{ID: id, Realm: name})
		}
		_ = json.NewEncoder(w).Encode(list)
	case len(parts) == 4 && parts[3] == "events":
		realm := parts[2]
		assert.Equal(f.t, "LOGIN", r.URL.Query().Get("type"))
		assert.Equal(f.t, "asc", r.URL.Query().Get("direction"))
		from, _ := strconv.ParseInt(r.URL.Query().Get("dateFrom"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("max"))
		f.dateFroms[realm] = append(f.dateFroms[realm], from)
		out := []kc.Event{}
		for _, ev := range f.events[realm] {
			if ev.Time >= from && len(out) < limit {
				out = append(out, ev)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case len(parts) == 5 && parts[3] == "users":
		u, ok := f.users[parts[4]]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(u)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeKeycloak) addRealm(name, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.realms[name] = id
}

func (f *fakeKeycloak) login(realm, id string, at int64, userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events[realm] = append(f.events[realm], kc.Event{ID: id, Time: at, Type: "LOGIN", UserID: userID,
		Details: map[string]string{"username": "Alice@Example.com"}})
}

type harness struct {
	fake      *fakeKeycloak
	kube      ctrlruntimeclient.Client
	published []cloudevents.Event
	// failAfter makes Publish fail once this many events were accepted; -1 never.
	failAfter int
	now       time.Time
	claim     string
}

func newHarness(t *testing.T) *harness {
	f := newFakeKeycloak(t)
	f.users["u-alice"] = kc.User{ID: "u-alice", Username: "alice@example.com", FirstName: "Alice"}
	return &harness{fake: f, kube: fake.NewClientBuilder().Build(), failAfter: -1, now: time.UnixMilli(1000), claim: ClaimEmail}
}

// adapter returns a new adapter on the same fake Keycloak and the same state.
func (h *harness) adapter(t *testing.T) *Adapter {
	srv := httptest.NewServer(h.fake)
	t.Cleanup(srv.Close)
	client := kc.NewClient(context.Background(), srv.URL, "reader", "s3cret")
	return &Adapter{
		KC:         client,
		Users:      &kc.Users{Client: client},
		SkipRealms: []string{"master", "welcome"},
		UserClaim:  h.claim,
		Kube:       h.kube,
		Namespace:  "contrib-eventing",
		Log:        testlogger.New().HideLogOutput().Logger,
		now:        func() time.Time { return h.now },
		Publish: func(_ context.Context, e cloudevents.Event) error {
			if h.failAfter >= 0 && len(h.published) >= h.failAfter {
				return errors.New("ingress down")
			}
			h.published = append(h.published, e)
			return nil
		},
	}
}

func loadedState(t *testing.T, a *Adapter) map[string]realmState {
	_, state, err := a.loadState(context.Background())
	require.NoError(t, err)
	return state
}

func TestFirstRunStartsNow(t *testing.T) {
	h := newHarness(t)
	h.fake.addRealm("acme", "id-acme")
	h.fake.login("acme", "old", 500, "u-alice")
	h.fake.login("master", "m1", 1500, "u-alice")
	h.fake.login("welcome", "w1", 1500, "u-alice")
	a := h.adapter(t)
	ctx := context.Background()

	require.NoError(t, a.poll(ctx))
	assert.Empty(t, h.published, "logins from before the first run are not published")
	assert.Equal(t, []int64{1000}, h.fake.dateFroms["acme"])
	assert.NotContains(t, h.fake.dateFroms, "master", "master is skipped")
	assert.NotContains(t, h.fake.dateFroms, "welcome", "welcome is skipped")
	assert.Equal(t, realmState{RealmID: "id-acme", Since: 1000}, loadedState(t, a)["acme"])

	// A login after the start is published, with the org, the user and the login time.
	h.fake.login("acme", "new", 1500, "u-alice")
	require.NoError(t, a.poll(ctx))
	require.Len(t, h.published, 1)
	e := h.published[0]
	assert.Equal(t, contract.TypeUserLoggedIn, e.Type())
	assert.Equal(t, "users/alice@example.com", e.Subject())
	assert.Equal(t, "acme", e.Extensions()[contract.OrgExtension])
	assert.Equal(t, int64(1500), e.Time().UnixMilli())
	assert.JSONEq(t, `{"org":"acme","user":{"id":"alice@example.com"}}`, string(e.Data()))

	// A realm that appears later starts at its beginning.
	h.published = nil
	h.fake.addRealm("beta", "id-beta")
	h.fake.login("beta", "b1", 100, "u-alice")
	require.NoError(t, a.poll(ctx))
	require.Len(t, h.published, 1)
	assert.Equal(t, "beta", h.published[0].Extensions()[contract.OrgExtension])
	assert.Equal(t, int64(0), h.fake.dateFroms["beta"][0])
}

func TestInclusiveBoundary(t *testing.T) {
	h := newHarness(t)
	h.fake.addRealm("acme", "id-acme")
	a := h.adapter(t)
	ctx := context.Background()
	require.NoError(t, a.poll(ctx))

	// Two logins in the same millisecond, seen in two different polls.
	h.fake.login("acme", "e1", 2000, "u-alice")
	require.NoError(t, a.poll(ctx))
	h.fake.login("acme", "e2", 2000, "u-alice")
	require.NoError(t, a.poll(ctx))
	require.NoError(t, a.poll(ctx))

	require.Len(t, h.published, 2, "each once, although Keycloak returns e1 again")
	assert.NotEqual(t, h.published[0].ID(), h.published[1].ID())
	assert.Equal(t, realmState{RealmID: "id-acme", Since: 2000, Seen: []string{"e1", "e2"}}, loadedState(t, a)["acme"])

	h.fake.login("acme", "e3", 2001, "u-alice")
	require.NoError(t, a.poll(ctx))
	require.Len(t, h.published, 3)
	assert.Equal(t, realmState{RealmID: "id-acme", Since: 2001, Seen: []string{"e3"}}, loadedState(t, a)["acme"], "seen starts again when time moves on")
}

func TestManyPages(t *testing.T) {
	h := newHarness(t)
	h.fake.addRealm("acme", "id-acme")
	a := h.adapter(t)
	ctx := context.Background()
	require.NoError(t, a.poll(ctx))

	// More than a page, with runs in one millisecond longer than a page.
	for i := range 130 {
		h.fake.login("acme", fmt.Sprintf("e%03d", i), 2000+int64(i/60), "u-alice")
	}
	require.NoError(t, a.poll(ctx))
	require.Len(t, h.published, 130)
	unique := map[string]bool{}
	for _, e := range h.published {
		unique[e.ID()] = true
	}
	assert.Len(t, unique, 130, "all ids differ")
	require.NoError(t, a.poll(ctx))
	assert.Len(t, h.published, 130, "nothing twice")
}

func TestFailedPageDoesNotMoveCursor(t *testing.T) {
	h := newHarness(t)
	h.fake.addRealm("acme", "id-acme")
	a := h.adapter(t)
	ctx := context.Background()
	require.NoError(t, a.poll(ctx))

	h.fake.login("acme", "e1", 2000, "u-alice")
	h.fake.login("acme", "e2", 2001, "u-alice")
	h.fake.login("acme", "e3", 2002, "u-alice")
	h.failAfter = 1
	require.NoError(t, a.poll(ctx), "a publish error is logged and retried, not a failed poll")
	require.Len(t, h.published, 1)
	assert.Equal(t, realmState{RealmID: "id-acme", Since: 1000}, loadedState(t, a)["acme"], "the cursor stays")
	firstTry := h.published[0].ID()

	h.failAfter, h.published = -1, nil
	require.NoError(t, a.poll(ctx))
	require.Len(t, h.published, 3)
	assert.Equal(t, firstTry, h.published[0].ID(), "a retried event keeps its id")
	assert.Equal(t, int64(2002), loadedState(t, a)["acme"].Since)
}

func TestRestart(t *testing.T) {
	h := newHarness(t)
	h.fake.addRealm("acme", "id-acme")
	require.NoError(t, h.adapter(t).poll(context.Background()))
	h.fake.login("acme", "e1", 2000, "u-alice")
	require.NoError(t, h.adapter(t).poll(context.Background()))
	require.Len(t, h.published, 1)

	// A new adapter on the same state publishes nothing again.
	require.NoError(t, h.adapter(t).poll(context.Background()))
	assert.Len(t, h.published, 1)
	h.fake.login("acme", "e2", 2000, "u-alice")
	require.NoError(t, h.adapter(t).poll(context.Background()))
	assert.Len(t, h.published, 2)
}

func TestRecreatedRealmStartsOver(t *testing.T) {
	h := newHarness(t)
	h.fake.addRealm("acme", "id-acme")
	a := h.adapter(t)
	ctx := context.Background()
	require.NoError(t, a.poll(ctx))
	h.fake.login("acme", "e1", 2000, "u-alice")
	require.NoError(t, a.poll(ctx))
	require.Len(t, h.published, 1)

	h.published = nil
	h.fake.mu.Lock()
	h.fake.realms["acme"] = "id-acme-2"
	h.fake.events["acme"] = []kc.Event{{ID: "n1", Time: 10, Type: "LOGIN", UserID: "u-alice"}}
	h.fake.mu.Unlock()
	require.NoError(t, a.poll(ctx))
	require.Len(t, h.published, 1, "the new realm is read from its beginning")
	assert.Equal(t, "id-acme-2", loadedState(t, a)["acme"].RealmID)
}

func TestUserClaim(t *testing.T) {
	for _, tc := range []struct {
		name, claim, userID, want string
	}{
		{"email is the username", ClaimEmail, "u-alice", "alice@example.com"},
		{"sub is the Keycloak id", ClaimSub, "u-alice", "u-alice"},
		{"a deleted user falls back to the recorded username", ClaimEmail, "u-deleted", "alice@example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.claim = tc.claim
			h.fake.addRealm("acme", "id-acme")
			a := h.adapter(t)
			require.NoError(t, a.poll(context.Background()))
			h.fake.login("acme", "e1", 2000, tc.userID)
			require.NoError(t, a.poll(context.Background()))
			require.Len(t, h.published, 1)
			var data contract.UserLoggedIn
			require.NoError(t, json.Unmarshal(h.published[0].Data(), &data))
			assert.Equal(t, tc.want, data.User.ID)
			assert.Equal(t, "users/"+tc.want, h.published[0].Subject())
		})
	}
}

func TestLoginWithoutUserIsDropped(t *testing.T) {
	h := newHarness(t)
	h.fake.addRealm("acme", "id-acme")
	a := h.adapter(t)
	require.NoError(t, a.poll(context.Background()))
	h.fake.login("acme", "e1", 2000, "")
	h.fake.login("acme", "e2", 2001, "u-alice")
	require.NoError(t, a.poll(context.Background()))
	require.Len(t, h.published, 1)
	assert.Equal(t, int64(2001), loadedState(t, a)["acme"].Since, "the cursor passes the dropped one")
}

func TestDeletedRealmIsDropped(t *testing.T) {
	h := newHarness(t)
	h.fake.addRealm("acme", "id-acme")
	h.fake.addRealm("beta", "id-beta")
	a := h.adapter(t)
	ctx := context.Background()
	require.NoError(t, a.poll(ctx))
	assert.Len(t, loadedState(t, a), 2)

	h.fake.mu.Lock()
	delete(h.fake.realms, "beta")
	h.fake.mu.Unlock()
	require.NoError(t, a.poll(ctx))
	assert.Contains(t, loadedState(t, a), "acme")
	assert.NotContains(t, loadedState(t, a), "beta")
}
