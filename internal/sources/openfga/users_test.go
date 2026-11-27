/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"context"
	"encoding/json"
	"errors"
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
	"go.platform-mesh.io/golang-commons/logger/testlogger"
)

// changelogFGA is OpenFGA with one changelog per store. The tuples are what
// the changelog leads to, so they always agree with it.
type changelogFGA struct {
	mu   sync.Mutex
	logs map[string][]Change // store -> changes
	// onReadUser runs once, before the first read by user is answered.
	onReadUser func()
	// failUserReads fails every read by user.
	failUserReads bool
}

func (f *changelogFGA) add(store string, write bool, user, object string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op := "TUPLE_OPERATION_DELETE"
	if write {
		op = opWrite
	}
	at := time.Date(2026, 11, 1, 0, 0, len(f.logs[store]), 0, time.UTC)
	f.logs[store] = append(f.logs[store], Change{TupleKey: TupleKey{User: "user:" + user, Relation: "assignee", Object: object}, Operation: op, Timestamp: at})
}

func (f *changelogFGA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	store := strings.Split(r.URL.Path, "/")[2]
	if strings.HasSuffix(r.URL.Path, "/changes") {
		f.mu.Lock()
		defer f.mu.Unlock()
		log := f.logs[store]
		from, _ := strconv.Atoi(r.URL.Query().Get("continuation_token"))
		size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		to := min(from+size, len(log))
		_ = json.NewEncoder(w).Encode(map[string]any{"changes": log[from:to], "continuation_token": strconv.Itoa(to)})
		return
	}
	var req struct {
		TupleKey TupleKey `json:"tuple_key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.TupleKey.User != "" && f.failUserReads {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	if req.TupleKey.User != "" && f.onReadUser != nil {
		hook := f.onReadUser
		f.onReadUser = nil
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := map[TupleKey]bool{}
	for _, c := range f.logs[store] {
		now[c.TupleKey] = c.Operation == opWrite
	}
	out := []any{}
	for k, ok := range now {
		if ok && (k.User == req.TupleKey.User || k.Object == req.TupleKey.Object) && strings.HasPrefix(k.Object, req.TupleKey.Object) {
			out = append(out, map[string]any{"key": k})
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"tuples": out})
}

func role(account, r string) string {
	return "role:core_platform-mesh_io_account/c1/" + account + "/" + r
}

type userHarness struct {
	fga       *changelogFGA
	adapter   *Adapter
	published []cloudevents.Event
	failAt    int
}

func newUserHarness(t *testing.T) *userHarness {
	h := &userHarness{fga: &changelogFGA{logs: map[string][]Change{}}, failAt: -1}
	srv := httptest.NewServer(h.fga)
	t.Cleanup(srv.Close)
	h.adapter = &Adapter{
		FGA:       &Client{BaseURL: srv.URL, HTTP: srv.Client()},
		UserClaim: claimEmail,
		Log:       testlogger.New().HideLogOutput().Logger,
		Publish: func(_ context.Context, e cloudevents.Event) error {
			if len(h.published) == h.failAt {
				return errors.New("ingress down")
			}
			h.published = append(h.published, e)
			return nil
		},
	}
	return h
}

// userEvents are the published user events as "created alice" and so on.
func (h *userHarness) userEvents() []string {
	var out []string
	for _, e := range h.published {
		user := strings.TrimPrefix(e.Subject(), "users/")
		switch e.Type() {
		case contract.TypeUserCreated:
			out = append(out, "created "+user)
		case contract.TypeUserDeleted:
			out = append(out, "deleted "+user)
		}
	}
	return out
}

func (h *userHarness) run(t *testing.T, org string, st *orgState) {
	t.Helper()
	_, err := h.adapter.publishNew(context.Background(), org, st)
	require.NoError(t, err)
}

func TestFirstAndLastRole(t *testing.T) {
	h := newUserHarness(t)
	h.fga.add("s-acme", true, "alice", role("team-a", "member"))
	h.fga.add("s-acme", true, "alice", role("team-b", "owner"))
	h.fga.add("s-acme", false, "alice", role("team-a", "member"))
	h.fga.add("s-acme", true, "bob", role("team-a", "member"))
	h.fga.add("s-acme", false, "alice", role("team-b", "owner"))
	h.fga.add("s-acme", true, "alice", role("team-a", "owner"))
	st := orgState{StoreID: "s-acme"}
	h.run(t, "acme", &st)
	assert.Equal(t, []string{"created alice", "created bob", "deleted alice", "created alice"}, h.userEvents(),
		"a second role and removing one of two say nothing; losing the last and getting one again do")

	// Later changes are read with the state the earlier ones left.
	h.published = nil
	h.fga.add("s-acme", true, "bob", role("team-b", "member"))
	h.fga.add("s-acme", false, "bob", role("team-a", "member"))
	h.fga.add("s-acme", false, "bob", role("team-b", "member"))
	h.run(t, "acme", &st)
	assert.Equal(t, []string{"deleted bob"}, h.userEvents())

	// The same user in another org joins that org, too.
	h.published = nil
	h.fga.add("s-beta", true, "alice", role("team-x", "member"))
	h.run(t, "beta", &orgState{StoreID: "s-beta"})
	assert.Equal(t, []string{"created alice"}, h.userEvents())
}

// The created and deleted events surround the change that made them, with the
// user's email under the email claim, and keep their ids when they are sent
// again.
func TestUserEventsOrderAndIDs(t *testing.T) {
	h := newUserHarness(t)
	h.fga.add("s-acme", true, "alice@example.com", role("team-a", "member"))
	h.fga.add("s-acme", false, "alice@example.com", role("team-a", "member"))
	st := orgState{StoreID: "s-acme"}
	h.run(t, "acme", &st)
	types := make([]string, 0, len(h.published))
	for _, e := range h.published {
		types = append(types, strings.TrimPrefix(e.Type(), "io.platform-mesh.iam."))
	}
	assert.Equal(t, []string{"user.created.v1alpha1", "membership.granted.v1alpha1", "membership.revoked.v1alpha1", "user.deleted.v1alpha1", "account.members.v1alpha1"}, types)
	assert.JSONEq(t, `{"org":"acme","user":{"id":"alice@example.com","email":"alice@example.com"}}`, string(h.published[0].Data()))
	assert.JSONEq(t, `{"org":"acme","user":{"id":"alice@example.com"}}`, string(h.published[3].Data()))

	first := h.published
	h.published = nil
	h.run(t, "acme", &orgState{StoreID: "s-acme"})
	for i := range first {
		assert.Equal(t, first[i].ID(), h.published[i].ID())
	}
}

// A page read again after a restart, when the user has more roles by then,
// still has their created event, with the same id.
func TestUserCreatedAfterRestart(t *testing.T) {
	h := newUserHarness(t)
	h.fga.add("s-acme", true, "alice", role("team-a", "member"))
	st := orgState{StoreID: "s-acme"}
	h.failAt = 1
	_, err := h.adapter.publishNew(context.Background(), "acme", &st)
	require.Error(t, err)
	require.Equal(t, []string{"created alice"}, h.userEvents())
	created := h.published[0].ID()
	assert.Empty(t, st.Token)

	h.fga.add("s-acme", true, "alice", role("team-b", "member"))
	h.published, h.failAt = nil, -1
	h.run(t, "acme", &st)
	assert.Equal(t, []string{"created alice"}, h.userEvents())
	assert.Equal(t, created, h.published[0].ID())
}

// A role written while the adapter reads the users' roles is read with them.
func TestChangelogMovesWhileReadingRoles(t *testing.T) {
	h := newUserHarness(t)
	h.fga.add("s-acme", true, "alice", role("team-a", "member"))
	h.fga.onReadUser = func() { h.fga.add("s-acme", true, "alice", role("team-b", "member")) }
	st := orgState{StoreID: "s-acme"}
	h.run(t, "acme", &st)
	assert.Equal(t, []string{"created alice"}, h.userEvents(), "team-b is her second role")
	assert.Equal(t, "2", st.Token)
}

// If the roles can't be read, nothing of the org goes out and the token
// stays, so the next poll reads the same changes again.
func TestRolesUnreadable(t *testing.T) {
	h := newUserHarness(t)
	h.fga.add("s-acme", true, "alice", role("team-a", "member"))
	st := orgState{StoreID: "s-acme"}

	h.fga.failUserReads = true
	_, err := h.adapter.publishNew(context.Background(), "acme", &st)
	require.Error(t, err)
	assert.Empty(t, h.published)
	assert.Empty(t, st.Token)

	// A changelog that changes on every read of the roles gives up, too.
	h.fga.failUserReads = false
	var write func()
	write = func() {
		h.fga.add("s-acme", true, "alice", role("team-b", "member"))
		h.fga.onReadUser = write
	}
	h.fga.onReadUser = write
	_, err = h.adapter.publishNew(context.Background(), "acme", &st)
	require.ErrorContains(t, err, "kept changing")
	assert.Empty(t, h.published)
	assert.Empty(t, st.Token)

	h.fga.onReadUser = nil
	h.run(t, "acme", &st)
	assert.Equal(t, []string{"created alice"}, h.userEvents())
}

// Joins and leaves span pages, and a crash between pages sends the second
// page's events again with the same ids.
func TestUserEventsAcrossPages(t *testing.T) {
	h := newUserHarness(t)
	for i := range pageSize - 2 {
		h.fga.add("s-acme", true, "u"+strconv.Itoa(i), role("team-a", "member"))
	}
	h.fga.add("s-acme", true, "alice", role("team-a", "member"))
	h.fga.add("s-acme", true, "dave", role("team-a", "member"))
	// Page 2.
	h.fga.add("s-acme", false, "alice", role("team-a", "member"))
	h.fga.add("s-acme", true, "dave", role("team-b", "owner"))
	h.fga.add("s-acme", true, "erin", role("team-b", "member"))

	h.run(t, "acme", &orgState{StoreID: "s-acme"})
	all := h.userEvents()
	require.Len(t, all, pageSize+2)
	assert.Equal(t, []string{"created dave", "deleted alice", "created erin"}, all[pageSize-1:])
	page2 := eventIDs(h.published[2*pageSize+1:])

	// The first page goes out, the second fails after alice's revoke.
	h.published, h.failAt = nil, 2*pageSize+2
	st := orgState{StoreID: "s-acme"}
	_, err := h.adapter.publishNew(context.Background(), "acme", &st)
	require.Error(t, err)
	assert.Equal(t, strconv.Itoa(pageSize), st.Token)

	h.published, h.failAt = nil, -1
	h.run(t, "acme", &st)
	assert.Equal(t, []string{"deleted alice", "created erin"}, h.userEvents())
	assert.Equal(t, page2, eventIDs(h.published))
}

func eventIDs(events []cloudevents.Event) []string {
	ids := make([]string, 0, len(events))
	for _, e := range events {
		ids = append(ids, e.ID())
	}
	return ids
}

// Names on created users follow the snapshot rules: all or none, with the
// error when Keycloak can't be read.
func TestUserCreatedProfile(t *testing.T) {
	h := newUserHarness(t)
	h.fga.add("s-acme", true, "alice@example.com", role("team-a", "member"))
	h.adapter.Profiles = fakeProfiles{"alice@example.com": {FirstName: "Alice"}}
	h.run(t, "acme", &orgState{StoreID: "s-acme"})
	assert.JSONEq(t, `{"org":"acme","user":{"id":"alice@example.com","email":"alice@example.com","firstName":"Alice"}}`, string(h.published[0].Data()))

	h.published = nil
	h.adapter.Profiles = fakeProfiles{}
	h.run(t, "acme", &orgState{StoreID: "s-acme"})
	assert.JSONEq(t, `{"org":"acme","user":{"id":"alice@example.com","email":"alice@example.com"},"errors":["namesUnavailable"]}`, string(h.published[0].Data()))
}

// fakeProfiles knows some users and fails for the others.
type fakeProfiles map[string]Profile

func (f fakeProfiles) Profile(_ context.Context, _, user string) (Profile, error) {
	p, ok := f[user]
	if !ok {
		return Profile{}, errors.New("keycloak down")
	}
	return p, nil
}
