/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"context"
	"encoding/json"
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
	"github.com/platform-mesh/contrib-eventing/internal/keycloak"
	"go.platform-mesh.io/golang-commons/logger/testlogger"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// fakeBackends is OpenFGA and Keycloak on one server. OpenFGA has the store
// s-acme with the tuples above and no changes. Keycloak has the realms in
// logins, users alice and bob, and the stored logins.
type fakeBackends struct {
	mu       sync.Mutex
	logins   map[string][]keycloak.Event // realm -> logins
	keycloak bool                        // false: Keycloak answers 503
	changes  string
}

func (f *fakeBackends) login(realm, id string, at int64, userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins[realm] = append(f.logins[realm], keycloak.Event{ID: id, Time: at, Type: "LOGIN", UserID: userID})
}

func (f *fakeBackends) setKeycloak(up bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keycloak = up
}

func (f *fakeBackends) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/stores":
		_, _ = fmt.Fprint(w, `{"stores": [{"id": "s-acme", "name": "acme"}]}`)
	case strings.HasSuffix(r.URL.Path, "/changes") && r.URL.Query().Get("continuation_token") == "after-page":
		_, _ = fmt.Fprint(w, `{"changes": [], "continuation_token": "after-page"}`)
	case strings.HasSuffix(r.URL.Path, "/changes"):
		_, _ = fmt.Fprint(w, f.changes)
	case strings.HasSuffix(r.URL.Path, "/read"):
		f.serveRead(w, r)
	case r.URL.Path == "/realms/master/protocol/openid-connect/token":
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"tok","token_type":"Bearer","expires_in":300}`)
	case !f.keycloak:
		http.Error(w, "down", http.StatusServiceUnavailable)
	case strings.HasSuffix(r.URL.Path, "/events"):
		f.serveEvents(w, r)
	case r.URL.Path == "/admin/realms/acme/users/k-alice":
		_, _ = fmt.Fprint(w, `{"id":"k-alice","username":"alice@example.com","firstName":"Alice"}`)
	default:
		http.NotFound(w, r)
	}
}

// serveRead answers reads by object from tuples, and reads by user from all
// of them.
func (f *fakeBackends) serveRead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TupleKey TupleKey `json:"tuple_key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	var out []any
	if req.TupleKey.User != "" {
		for _, k := range userTuples(req.TupleKey.User, req.TupleKey.Object) {
			out = append(out, map[string]any{"key": k})
		}
	} else {
		for _, k := range tuples[req.TupleKey.Object] {
			out = append(out, map[string]any{"key": k})
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"tuples": out})
}

// serveEvents filters like Keycloak: dateFrom and dateTo inclusive, user.
func (f *fakeBackends) serveEvents(w http.ResponseWriter, r *http.Request) {
	realm := strings.Split(r.URL.Path, "/")[3]
	events, ok := f.logins[realm]
	if !ok {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	from, _ := strconv.ParseInt(q.Get("dateFrom"), 10, 64)
	to, err := strconv.ParseInt(q.Get("dateTo"), 10, 64)
	if err != nil {
		to = 1 << 62
	}
	limit, _ := strconv.Atoi(q.Get("max"))
	out := []keycloak.Event{}
	for _, ev := range events {
		if ev.Time >= from && ev.Time <= to && (q.Get("user") == "" || q.Get("user") == ev.UserID) && len(out) < limit {
			out = append(out, ev)
		}
	}
	_ = json.NewEncoder(w).Encode(out)
}

type loginHarness struct {
	backends  *fakeBackends
	adapter   *Adapter
	published []cloudevents.Event
}

func newLoginHarness(t *testing.T, claim string, objs ...*corev1.ConfigMap) *loginHarness {
	h := &loginHarness{backends: &fakeBackends{
		logins:   map[string][]keycloak.Event{"acme": nil},
		keycloak: true,
		changes:  `{"changes": []}`,
	}}
	srv := httptest.NewServer(h.backends)
	t.Cleanup(srv.Close)
	kube := fake.NewClientBuilder()
	for _, o := range objs {
		kube = kube.WithObjects(o)
	}
	h.adapter = &Adapter{
		FGA:       &Client{BaseURL: srv.URL, HTTP: srv.Client()},
		UserClaim: claim,
		Logins:    &KeycloakLogins{KC: keycloak.NewClient(context.Background(), srv.URL, "reader", "s3cret"), UserClaim: claim},
		Kube:      kube.Build(),
		Namespace: "contrib-eventing",
		Log:       testlogger.New().HideLogOutput().Logger,
		Publish: func(_ context.Context, e cloudevents.Event) error {
			h.published = append(h.published, e)
			return nil
		},
	}
	return h
}

func (h *loginHarness) snapshots() []contract.AccountMembers {
	var out []contract.AccountMembers
	for _, e := range h.published {
		if e.Type() == contract.TypeAccountMembers {
			var m contract.AccountMembers
			_ = json.Unmarshal(e.Data(), &m)
			out = append(out, m)
		}
	}
	return out
}

func TestFirstLoginSendsMembers(t *testing.T) {
	h := newLoginHarness(t, "email")
	ctx := context.Background()
	require.NoError(t, h.adapter.poll(ctx))
	now := time.Now().UnixMilli()

	h.backends.login("acme", "l1", now+10, "k-alice")
	require.NoError(t, h.adapter.poll(ctx))
	snaps := h.snapshots()
	require.Len(t, snaps, 1, "alice has two roles on one account, and mallory's role elsewhere is not hers")
	assert.Equal(t, "c1/team-a", snaps[0].AccountID)
	assert.Equal(t, contract.User{ID: "alice@example.com", Email: "alice@example.com"}, snaps[0].Members[0].User)
	assert.Equal(t, now+10, loadedState(t, h.adapter)["acme"].Logins.Since)

	h.backends.login("acme", "l2", now+20, "k-alice")
	require.NoError(t, h.adapter.poll(ctx))
	assert.Len(t, h.snapshots(), 1, "a second login sends nothing")

	// Bob has never logged in before, but Keycloak doesn't know him by now: the
	// username from the event is used.
	h.backends.mu.Lock()
	h.backends.logins["acme"] = append(h.backends.logins["acme"], keycloak.Event{ID: "l3", Time: now + 30, Type: "LOGIN", UserID: "k-bob", Details: map[string]string{"username": "Bob@example.com"}})
	h.backends.mu.Unlock()
	require.NoError(t, h.adapter.poll(ctx))
	require.Len(t, h.snapshots(), 2)
	assert.Equal(t, "c1/team-a", h.snapshots()[1].AccountID)
}

func TestFirstLoginUnderSub(t *testing.T) {
	tuples["role:core_platform-mesh_io_account/c9/other/owner"] = append(tuples["role:core_platform-mesh_io_account/c9/other/owner"],
		TupleKey{User: "user:k-carol", Relation: "assignee", Object: "role:core_platform-mesh_io_account/c9/other/owner"})
	t.Cleanup(func() {
		tuples["role:core_platform-mesh_io_account/c9/other/owner"] = tuples["role:core_platform-mesh_io_account/c9/other/owner"][:1]
	})
	h := newLoginHarness(t, "sub")
	ctx := context.Background()
	require.NoError(t, h.adapter.poll(ctx))
	h.backends.login("acme", "l1", time.Now().UnixMilli()+10, "k-carol")
	require.NoError(t, h.adapter.poll(ctx))
	require.Len(t, h.snapshots(), 1, "the tuples have the Keycloak id")
	assert.Equal(t, "c9/other", h.snapshots()[0].AccountID)
}

// A Keycloak outage must not hold membership changes back, and the logins
// wait for it.
func TestFirstLoginKeycloakDown(t *testing.T) {
	h := newLoginHarness(t, "email")
	ctx := context.Background()
	require.NoError(t, h.adapter.poll(ctx))
	since := loadedState(t, h.adapter)["acme"].Logins.Since
	h.backends.login("acme", "l1", time.Now().UnixMilli()+10, "k-alice")

	h.backends.setKeycloak(false)
	h.backends.changes = threeChanges
	require.NoError(t, h.adapter.poll(ctx))
	assert.Len(t, h.published, 7, "three changes, two user events and two snapshots, no login")
	assert.Equal(t, since, loadedState(t, h.adapter)["acme"].Logins.Since, "the login cursor stays")

	h.backends.changes = `{"changes": []}`
	h.backends.setKeycloak(true)
	require.NoError(t, h.adapter.poll(ctx))
	assert.Len(t, h.published, 8, "the login's snapshot follows")
}

// A Keycloak that hangs delays no membership events, of any org.
func TestFirstLoginKeycloakHangs(t *testing.T) {
	h := newLoginHarness(t, "email")
	h.backends.changes = threeChanges
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/stores":
			_, _ = fmt.Fprint(w, `{"stores": [{"id": "s-acme", "name": "acme"}, {"id": "s-beta", "name": "beta"}]}`)
		case strings.HasSuffix(r.URL.Path, "/events"):
			<-r.Context().Done()
		default:
			h.backends.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	h.adapter.FGA = &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	h.adapter.Logins.KC = keycloak.NewClient(context.Background(), srv.URL, "reader", "s3cret")
	h.adapter.LoginsTimeout = 300 * time.Millisecond

	start := time.Now()
	require.NoError(t, h.adapter.poll(context.Background()))
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Len(t, h.published, 14, "the changes of both orgs")
	state := loadedState(t, h.adapter)
	assert.NotEmpty(t, state["acme"].Token)
	assert.NotEmpty(t, state["beta"].Token)
}

// State from before logins were read loads, and the logins start now, not at
// the beginning of the realm.
func TestFirstLoginOldState(t *testing.T) {
	old := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "contrib-eventing", Name: StateConfigMap},
		Data:       map[string]string{"acme": `{"storeID":"s-acme","token":"t1"}`},
	}
	h := newLoginHarness(t, "email", old)
	h.backends.login("acme", "l-old", 1000, "k-alice")
	require.NoError(t, h.adapter.poll(context.Background()))
	assert.Empty(t, h.snapshots())
	st := loadedState(t, h.adapter)["acme"]
	assert.Equal(t, "t1", st.Token)
	assert.WithinDuration(t, time.Now(), time.UnixMilli(st.Logins.Since), time.Minute)
}

// An org whose realm doesn't exist has no logins; that is not an error.
func TestFirstLoginNoRealm(t *testing.T) {
	h := newLoginHarness(t, "email")
	h.backends.logins = map[string][]keycloak.Event{}
	require.NoError(t, h.adapter.poll(context.Background()))
	require.NoError(t, h.adapter.poll(context.Background()))
	assert.Empty(t, h.published)
}
