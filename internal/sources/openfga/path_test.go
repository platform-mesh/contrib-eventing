/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.platform-mesh.io/golang-commons/logger/testlogger"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// newPaths returns paths that know nothing yet, at a fixed time, with the
// watch stubbed: tests hand AccountInfos to the paths directly, the way the
// watch's event handler does, and say whether the watch has synced.
func newPaths(t *testing.T) (p *AccountPaths, synced *bool, now *time.Time) {
	synced, now = new(bool), new(time.Time)
	*synced, *now = true, time.Date(2026, 10, 23, 13, 0, 0, 0, time.UTC)
	p = &AccountPaths{
		Grace:       time.Minute,
		SyncTimeout: 5 * time.Minute,
		Log:         testlogger.New().HideLogOutput().Logger,
		synced:      func() bool { return *synced },
		now:         func() time.Time { return *now },
	}
	p.init()
	return p, synced, now
}

// accountInfo is an AccountInfo as the watch hands it over.
func accountInfo(cluster, name, path string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "core.platform-mesh.io/v1alpha1",
		"kind":       "AccountInfo",
		"metadata":   map[string]any{"name": "account"},
		"spec": map[string]any{"account": map[string]any{
			"name": name, "originClusterId": cluster, "path": path,
		}},
	}}
}

func TestAccountPaths(t *testing.T) {
	p, synced, now := newPaths(t)
	*synced = false

	// Orgs and nested accounts alike: each has its own AccountInfo. Known
	// accounts don't wait for the watch to sync.
	p.add(accountInfo("c-orgs", "acme", "root:orgs:acme"))
	p.add(accountInfo("c-acme", "team-a", "root:orgs:acme:team-a"))
	p.add(accountInfo("c-team-a", "team-a", "root:orgs:acme:team-a:team-a"))
	for id, want := range map[[2]string]string{
		{"c-orgs", "acme"}:     "root:orgs:acme",
		{"c-acme", "team-a"}:   "root:orgs:acme:team-a",
		{"c-team-a", "team-a"}: "root:orgs:acme:team-a:team-a",
	} {
		path, err := p.Path(id[0], id[1], *now)
		require.NoError(t, err)
		assert.Equal(t, want, path, id)
	}

	// An AccountInfo without the fields we need is ignored.
	p.add(accountInfo("c-acme", "", "root:orgs:acme:nameless"))
	assert.Len(t, p.paths, 3)
}

func TestAccountPathsUnknown(t *testing.T) {
	p, synced, now := newPaths(t)
	p.add(accountInfo("c-orgs", "acme", "root:orgs:acme"))

	// Until the watch has seen all AccountInfos, nothing is decided, not even
	// for old changes.
	*synced = false
	_, err := p.Path("c-acme", "team-a", now.Add(-time.Hour))
	require.ErrorContains(t, err, "not loaded yet")

	// A new account whose AccountInfo hasn't arrived yet: try again later.
	*synced = true
	changed := *now
	_, err = p.Path("c-acme", "team-a", changed)
	require.ErrorContains(t, err, "no AccountInfo for account c-acme/team-a yet")
	*now = now.Add(30 * time.Second)
	p.add(accountInfo("c-acme", "team-a", "root:orgs:acme:team-a"))
	path, err := p.Path("c-acme", "team-a", changed)
	require.NoError(t, err)
	assert.Equal(t, "root:orgs:acme:team-a", path)

	// An account that never shows up was deleted before we saw it: no path
	// once the change is older than the grace period.
	_, err = p.Path("c-acme", "gone", now.Add(-59*time.Second))
	require.Error(t, err)
	path, err = p.Path("c-acme", "gone", now.Add(-time.Minute))
	require.NoError(t, err)
	assert.Empty(t, path)
}

// After a restart the changelog may hold many changes of accounts deleted
// meanwhile. None of them waits: their changes are old.
func TestAccountPathsAfterRestart(t *testing.T) {
	p, _, now := newPaths(t)
	p.add(accountInfo("c-orgs", "acme", "root:orgs:acme"))
	for i := range 100 {
		path, err := p.Path("c-acme", fmt.Sprintf("deleted-%d", i), now.Add(-2*time.Hour))
		require.NoError(t, err)
		assert.Empty(t, path)
	}
}

// The informer of the provider counts as synced before it found any shard.
func TestAccountPathsNoShardYet(t *testing.T) {
	p, _, now := newPaths(t)
	_, err := p.Path("c-acme", "team-a", now.Add(-time.Hour))
	require.ErrorContains(t, err, "not loaded yet")
}

// A shard whose AccountInfos never load doesn't hold back changes forever.
func TestAccountPathsSyncTimeout(t *testing.T) {
	p, synced, now := newPaths(t)
	p.add(accountInfo("c-orgs", "acme", "root:orgs:acme"))
	*synced = false
	old := now.Add(-time.Hour)

	_, err := p.Path("c-acme", "team-a", old)
	require.ErrorContains(t, err, "not loaded yet")
	*now = now.Add(4 * time.Minute)
	_, err = p.Path("c-acme", "team-a", old)
	require.ErrorContains(t, err, "not loaded yet")
	*now = now.Add(time.Minute)
	path, err := p.Path("c-acme", "team-a", old)
	require.NoError(t, err)
	assert.Empty(t, path)
	assert.True(t, p.warned)

	// Once synced, a later stall waits again from the start.
	*synced = true
	_, err = p.Path("c-acme", "team-b", old)
	require.NoError(t, err)
	*synced = false
	_, err = p.Path("c-acme", "team-c", old)
	require.ErrorContains(t, err, "not loaded yet")
	assert.False(t, p.warned)
}

func TestPublishWithPaths(t *testing.T) {
	paths, _, _ := newPaths(t)
	paths.add(accountInfo("c-acme", "team-a", "root:orgs:acme:team-a"))
	fga := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/read") {
			_, _ = w.Write([]byte(`{"tuples": []}`))
			return
		}
		_, _ = w.Write([]byte(`{"changes": [
		  {"operation": "TUPLE_OPERATION_WRITE", "timestamp": "2026-10-23T13:43:24Z",
		   "tuple_key": {"user": "user:alice@example.com", "relation": "assignee", "object": "role:core_platform-mesh_io_account/c-acme/team-a/member"}}
		]}`))
	}))
	defer fga.Close()

	var data []string
	a := &Adapter{
		FGA:   &Client{BaseURL: fga.URL, HTTP: fga.Client()},
		Paths: paths,
		Log:   testlogger.New().HideLogOutput().Logger,
		Publish: func(_ context.Context, e cloudevents.Event) error {
			data = append(data, string(e.Data()))
			return nil
		},
	}
	_, err := a.publishNew(context.Background(), "acme", &orgState{StoreID: "s"})
	require.NoError(t, err)
	require.Len(t, data, 2)
	for _, d := range data {
		assert.Contains(t, d, `"accountPath":"root:orgs:acme:team-a"`)
	}
}
