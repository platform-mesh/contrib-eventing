/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package keycloak publishes logins read from Keycloak's event store. Keycloak
// cannot send events itself, but it stores them per realm and lists them from a
// given time on. Platform Mesh gives each org a realm named like the org.
package keycloak

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/platform-mesh/contrib-eventing/internal/contract"
	"github.com/platform-mesh/contrib-eventing/internal/keycloak"
	"go.platform-mesh.io/golang-commons/logger"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// StateConfigMap holds, per realm, how far the adapter got in the events.
	StateConfigMap = "eventing-keycloak-adapter-state"

	// ClaimEmail and ClaimSub are the user claims the adapter can follow.
	ClaimEmail = "email"
	ClaimSub   = "sub"

	loginType = "LOGIN"
)

// Adapter reads the login events of every org realm.
type Adapter struct {
	KC *keycloak.Client
	// SkipRealms are realms that do not belong to an org.
	SkipRealms []string
	// UserClaim is the claim Platform Mesh uses as user id: email or sub.
	UserClaim string
	Kube      ctrlruntimeclient.Client
	Namespace string
	Publish   func(ctx context.Context, e cloudevents.Event) error
	Interval  time.Duration
	Log       *logger.Logger

	// now can be replaced in tests.
	now func() time.Time
}

// realmState is stored as JSON in the state ConfigMap, one key per realm.
type realmState struct {
	// RealmID is the realm's Keycloak id. A recreated org gets a new one.
	RealmID string `json:"realmID,omitempty"`
	keycloak.Cursor
}

func (a *Adapter) Run(ctx context.Context) error {
	t := time.NewTicker(a.Interval)
	defer t.Stop()
	for {
		if err := a.poll(ctx); err != nil {
			a.Log.Error().Err(err).Msg("poll failed")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (a *Adapter) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a *Adapter) poll(ctx context.Context) error {
	realms, err := a.KC.Realms(ctx)
	if err != nil {
		return err
	}
	cm, state, err := a.loadState(ctx)
	if err != nil {
		return err
	}
	// On the very first run there is no state yet: realms that exist already
	// start now. Realms created later start at their beginning, so the first
	// login of a new org is not missed.
	firstRun := cm.ResourceVersion == ""

	// The first run always saves, even without orgs, so the next run knows it
	// is not the first.
	changed := firstRun
	found := map[string]bool{}
	for _, r := range realms {
		if slices.Contains(a.SkipRealms, r.Realm) {
			continue
		}
		found[r.Realm] = true
		st, known := state[r.Realm]
		fresh := !known || st.RealmID != r.ID
		if fresh {
			st = realmState{RealmID: r.ID}
			if firstRun {
				st.Since = a.clock().UnixMilli()
			}
		}
		moved, err := a.publishNew(ctx, r.Realm, &st)
		if err != nil {
			a.Log.Error().Err(err).Str("org", r.Realm).Msg("cannot publish logins")
		}
		if moved || fresh {
			state[r.Realm] = st
			changed = true
		}
	}
	// Forget realms that are gone.
	for realm := range state {
		if !found[realm] {
			delete(state, realm)
			changed = true
		}
	}
	if changed {
		return a.saveState(ctx, cm, state)
	}
	return nil
}

// publishNew publishes the realm's new logins. Consumers drop repeats of a page
// read again by event id.
func (a *Adapter) publishNew(ctx context.Context, realm string, st *realmState) (bool, error) {
	return a.KC.EachNew(ctx, realm, loginType, &st.Cursor, func(ev keycloak.Event) error {
		return a.publishLogin(ctx, realm, ev)
	})
}

// publishLogin sends one login. Logins without a user, which Keycloak records
// for unknown users, are dropped.
func (a *Adapter) publishLogin(ctx context.Context, realm string, ev keycloak.Event) error {
	if ev.UserID == "" {
		return nil
	}
	user, err := a.user(ctx, realm, ev)
	if err != nil {
		return err
	}
	if user.ID == "" {
		a.Log.Warn().Str("org", realm).Str("eventId", ev.ID).Msg("login without a usable user id, dropped")
		return nil
	}
	e, err := contract.LoggedIn(realm, user, ev.ID, time.UnixMilli(ev.Time))
	if err != nil {
		return err
	}
	if err := a.Publish(ctx, e); err != nil {
		return err
	}
	a.Log.Info().Str("org", realm).Str("type", e.Type()).Str("id", e.ID()).Msg("published")
	return nil
}

// user reads the event's user from Keycloak, so the event has the profile as
// it is now. If the read fails, the event waits for the next poll: nothing is
// sent without the profile. A user that is gone by now gets no profile and the
// username Keycloak recorded with the event.
func (a *Adapter) user(ctx context.Context, realm string, ev keycloak.Event) (contract.User, error) {
	u, found, err := a.KC.User(ctx, realm, ev.UserID)
	if err != nil {
		return contract.User{}, fmt.Errorf("reading user %s of %s: %w", ev.UserID, realm, err)
	}
	if !found {
		return contract.User{ID: keycloak.UserID(a.UserClaim, ev.UserID, ev.Details["username"])}, nil
	}
	return contract.User{ID: keycloak.UserID(a.UserClaim, u.ID, u.Username), Email: u.Email, FirstName: u.FirstName, LastName: u.LastName}, nil
}

func (a *Adapter) loadState(ctx context.Context) (*corev1.ConfigMap, map[string]realmState, error) {
	cm := &corev1.ConfigMap{}
	state := map[string]realmState{}
	err := a.Kube.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: a.Namespace, Name: StateConfigMap}, cm)
	if apierrors.IsNotFound(err) {
		cm.Namespace, cm.Name = a.Namespace, StateConfigMap
		return cm, state, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for realm, raw := range cm.Data {
		var st realmState
		if err := json.Unmarshal([]byte(raw), &st); err != nil {
			a.Log.Error().Err(err).Str("org", realm).Msg("ignoring broken state")
			continue
		}
		state[realm] = st
	}
	return cm, state, nil
}

func (a *Adapter) saveState(ctx context.Context, cm *corev1.ConfigMap, state map[string]realmState) error {
	cm.Data = map[string]string{}
	for realm, st := range state {
		b, err := json.Marshal(st)
		if err != nil {
			return err
		}
		cm.Data[realm] = string(b)
	}
	if cm.ResourceVersion == "" {
		return a.Kube.Create(ctx, cm)
	}
	return a.Kube.Update(ctx, cm)
}
