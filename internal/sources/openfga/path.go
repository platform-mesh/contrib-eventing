/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/platform-mesh/contrib-eventing/internal/contract"
	"go.platform-mesh.io/golang-commons/logger"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"

	"github.com/kcp-dev/multicluster-provider/apiexport"
	kcpapisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
)

var accountInfoGVK = schema.GroupVersionKind{Group: "core.platform-mesh.io", Version: "v1alpha1", Kind: "AccountInfo"}

// AccountPaths knows the kcp workspace path of every account, for example
// root:orgs:acme:team-a. Every account workspace has an AccountInfo with the
// path, and the originClusterId and name it gives for the account are our
// account id. AccountPaths watches them in all workspaces through the virtual
// workspace of the core.platform-mesh.io export, on every shard, the way
// iam-service does.
//
// Paths of deleted accounts stay, so the events about their members still
// carry the path. After a restart only the accounts that still exist are known.
type AccountPaths struct {
	// Config reaches the workspace of the APIExportEndpointSlice.
	Config        *rest.Config
	EndpointSlice string
	// Grace is how long after a change the account's AccountInfo may still be
	// missing because it is on its way. Changes younger than that are held back;
	// for older ones the account was deleted before we saw it, and their events
	// have no path.
	Grace time.Duration
	// SyncTimeout is how long we wait for the AccountInfos of all shards to load.
	// After that we stop waiting for a shard that doesn't answer: until it
	// catches up, its accounts count as unknown.
	SyncTimeout time.Duration
	Log         *logger.Logger

	// synced reports whether the watch has seen the AccountInfos of all
	// shards. Tests replace it.
	synced func() bool
	now    func() time.Time

	mu            sync.Mutex
	paths         map[string]string // by account id
	unsyncedSince time.Time
	warned        bool
}

// Start watches the AccountInfos until ctx is done.
func (p *AccountPaths) Start(ctx context.Context) error {
	p.init()
	scheme := runtime.NewScheme()
	// The provider reads the APIExportEndpointSlice and APIBindings with it.
	utilruntime.Must(kcpapisv1alpha1.AddToScheme(scheme))
	// The provider logs every workspace it sees; keep that for debug, errors
	// still show.
	providerLog := p.Log.ComponentLogger("kcp-provider").Logr().V(1)
	provider, err := apiexport.New(p.Config, p.EndpointSlice, apiexport.Options{Scheme: scheme, Log: &providerLog})
	if err != nil {
		return err
	}
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(accountInfoGVK)
	inf, err := provider.GetAggregateInformer(obj)
	if err != nil {
		return err
	}
	// Deletions are left out on purpose: the path of a deleted account stays.
	reg, err := inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    p.add,
		UpdateFunc: func(_, obj any) { p.add(obj) },
	})
	if err != nil {
		return err
	}
	p.synced = reg.HasSynced
	go func() {
		if err := provider.Start(ctx, nil); err != nil {
			p.Log.Error().Err(err).Msg("AccountInfo watch stopped")
		}
	}()
	return nil
}

func (p *AccountPaths) init() {
	p.paths = map[string]string{}
	if p.now == nil {
		p.now = time.Now
	}
}

// Path returns the path of the account in the given cluster, the cluster that
// holds the Account object, for a change made at the given time. It returns an
// error while the account isn't known yet but may still show up; the caller
// tries again later.
func (p *AccountPaths) Path(cluster, account string, at time.Time) (string, error) {
	id := contract.AccountID(cluster, account)
	if path, ok := p.lookup(id); ok {
		return path, nil
	}
	if !p.loaded() {
		return "", errors.New("account paths are not loaded yet")
	}
	// Loaded, so the account is either new and its AccountInfo on the way, or
	// deleted before we saw it.
	if p.now().Sub(at) < p.Grace {
		return "", fmt.Errorf("no AccountInfo for account %s yet", id)
	}
	return "", nil
}

// loaded reports whether all shards' AccountInfos are known, or we waited
// long enough for a shard that doesn't answer.
func (p *AccountPaths) loaded() bool {
	// The informer counts as synced while it has no shards yet. Every change we
	// look up is in an org, and every org has an AccountInfo, so seeing none
	// means the shards aren't there yet.
	synced := p.synced() && p.known() > 0
	p.mu.Lock()
	defer p.mu.Unlock()
	if synced {
		p.unsyncedSince, p.warned = time.Time{}, false
		return true
	}
	if p.unsyncedSince.IsZero() {
		p.unsyncedSince = p.now()
	}
	if p.now().Sub(p.unsyncedSince) < p.SyncTimeout {
		return false
	}
	if !p.warned {
		p.Log.Warn().Msg("AccountInfos of a shard don't load; its accounts get no path until they do")
		p.warned = true
	}
	return true
}

func (p *AccountPaths) lookup(id string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	path, ok := p.paths[id]
	return path, ok
}

func (p *AccountPaths) known() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.paths)
}

// add records the path from an AccountInfo.
func (p *AccountPaths) add(obj any) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}
	name, _, _ := unstructured.NestedString(u.Object, "spec", "account", "name")
	cluster, _, _ := unstructured.NestedString(u.Object, "spec", "account", "originClusterId")
	path, _, _ := unstructured.NestedString(u.Object, "spec", "account", "path")
	if name == "" || cluster == "" || path == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths[contract.AccountID(cluster, name)] = path
}
