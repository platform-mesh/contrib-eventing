/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package openfga publishes membership events read from the OpenFGA changelog.
// OpenFGA cannot send events itself, but its changelog contains every tuple
// write, no matter which component made it.
package openfga

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
	// StateConfigMap holds, per org, how far the adapter got in the changelog.
	StateConfigMap = "eventing-openfga-adapter-state"
	pageSize       = 50

	// The user claims Platform Mesh can use as user id.
	claimEmail = "email"
	claimSub   = "sub"
)

// Adapter reads the changelog of every org's OpenFGA store. Platform Mesh
// gives each org a store named like the org.
type Adapter struct {
	FGA *Client
	// Paths knows account paths. Without it events have no accountPath.
	Paths *AccountPaths
	// UserClaim is the claim Platform Mesh uses as user id: email or sub.
	UserClaim string
	// Profiles adds names, and under the sub claim emails, to the members of a
	// snapshot. Without it snapshots have neither, except the email under the
	// email claim, which is the user id.
	Profiles ProfileLookup
	// ProfileTimeout is the one deadline for all profile lookups of a snapshot.
	// Zero means DefaultProfileTimeout.
	ProfileTimeout time.Duration
	// Logins finds users' first logins, after which the adapter sends the
	// members of each account the user has a role on. Without it, it doesn't.
	Logins *KeycloakLogins
	// LoginsTimeout is the one deadline for reading the logins of all orgs in
	// a poll. Zero means defaultLoginsTimeout.
	LoginsTimeout time.Duration
	// SkipStores are stores that do not belong to an org.
	SkipStores []string
	Kube       ctrlruntimeclient.Client
	Namespace  string
	Publish    func(ctx context.Context, e cloudevents.Event) error
	Interval   time.Duration
	Log        *logger.Logger
}

// orgState is stored as JSON in the state ConfigMap, one key per org.
type orgState struct {
	// StoreID is the org's OpenFGA store. A recreated org gets a new store, and
	// tokens of the old one don't work there.
	StoreID string `json:"storeID,omitempty"`
	// Token is the OpenFGA continuation token after the last published change.
	Token string `json:"token,omitempty"`
	// Since is where reading starts while there is no token yet. Zero means the
	// beginning of the store.
	Since time.Time `json:"since,omitzero"`
	// Logins is how far the adapter got in the org's logins. Nil until it reads
	// them.
	Logins *keycloak.Cursor `json:"logins,omitempty"`
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

func (a *Adapter) poll(ctx context.Context) error {
	stores, err := a.FGA.StoreIDs(ctx)
	if err != nil {
		return err
	}
	cm, state, err := a.loadState(ctx)
	if err != nil {
		return err
	}
	// On the very first run there is no state yet: orgs that exist already start
	// now. Orgs created later start at the beginning of their store, so their
	// first changes, like the owner of a new org, are not missed.
	firstRun := cm.ResourceVersion == ""

	// The first run always saves, even without orgs, so the next run knows it
	// is not the first.
	changed := firstRun
	// The membership changes of every org go out first, so a slow Keycloak
	// only delays the logins.
	var orgs []string
	for org, storeID := range stores {
		if slices.Contains(a.SkipStores, org) {
			continue
		}
		st, known := state[org]
		fresh := !known || st.StoreID != storeID
		if fresh {
			st = orgState{StoreID: storeID}
			if firstRun {
				st.Since = time.Now()
			}
		}
		moved, err := a.publishNew(ctx, org, &st)
		if err != nil {
			a.Log.Error().Err(err).Str("org", org).Msg("cannot publish membership changes")
		}
		if a.Logins != nil && st.Logins == nil {
			// Like the changelog: orgs that are new to the adapter start at
			// their first login, others now.
			st.Logins = &keycloak.Cursor{}
			if !fresh || firstRun {
				st.Logins.Since = time.Now().UnixMilli()
			}
			moved = true
		}
		if moved || fresh {
			state[org] = st
			changed = true
		}
		orgs = append(orgs, org)
	}
	if a.Logins != nil && a.publishAllFirstLogins(ctx, orgs, state) {
		changed = true
	}
	// Forget orgs whose store is gone.
	for org := range state {
		if _, ok := stores[org]; !ok {
			delete(state, org)
			changed = true
		}
	}
	if changed {
		return a.saveState(ctx, cm, state)
	}
	return nil
}

// defaultLoginsTimeout is how long one poll reads logins, for all orgs.
const defaultLoginsTimeout = 30 * time.Second

// publishAllFirstLogins sends the members after first logins, org by org,
// within one deadline. Orgs it doesn't get to wait for the next poll. It
// returns whether a cursor moved.
func (a *Adapter) publishAllFirstLogins(ctx context.Context, orgs []string, state map[string]orgState) bool {
	timeout := a.LoginsTimeout
	if timeout == 0 {
		timeout = defaultLoginsTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	changed := false
	for _, org := range orgs {
		if ctx.Err() != nil {
			a.Log.Warn().Msg("logins took too long, the rest wait for the next poll")
			break
		}
		st := state[org]
		moved, err := a.publishFirstLogins(ctx, org, &st)
		// An org without a realm has no logins.
		if err != nil && !keycloak.IsNotFound(err) {
			a.Log.Warn().Err(err).Str("org", org).Msg("cannot publish members after first logins")
		}
		if moved {
			state[org] = st
			changed = true
		}
	}
	return changed
}

// publishNew publishes the org's new membership changes page by page. After
// the changes of a page it sends one members snapshot per account that
// changed. The token only moves after every event of a page was accepted; if
// one fails, the whole page is read again next time. Consumers drop the
// repeats by event id.
func (a *Adapter) publishNew(ctx context.Context, org string, st *orgState) (bool, error) {
	pages, err := a.readNew(ctx, st.StoreID, st.Token, st.Since)
	if err != nil {
		return false, err
	}
	u, err := a.joinsAndLeaves(ctx, st, pages)
	if err != nil {
		return false, err
	}
	moved := false
	for _, p := range u.pages {
		// The accounts that changed in this page, in order, with their last change.
		var accounts []membership
		last := map[string]Change{}
		for _, c := range p.changes {
			m, ok := parseMembership(c.TupleKey)
			if !ok {
				continue
			}
			key := changeKey(c)
			if u.joins[key] == joined {
				if err := a.publishCreated(ctx, org, m.user, key, c.Timestamp); err != nil {
					return moved, err
				}
			}
			if err := a.publishChange(ctx, org, m, c, key); err != nil {
				return moved, err
			}
			if u.joins[key] == left {
				e, err := contract.Deleted(org, m.user, key, c.Timestamp)
				if err == nil {
					err = a.publish(ctx, org, e)
				}
				if err != nil {
					return moved, err
				}
			}
			id := contract.AccountID(m.cluster, m.account)
			if _, seen := last[id]; !seen {
				accounts = append(accounts, m)
			}
			last[id] = c
		}
		for _, m := range accounts {
			c := last[contract.AccountID(m.cluster, m.account)]
			if err := a.publishMembers(ctx, org, st.StoreID, m, changeKey(c), c.Timestamp); err != nil {
				return moved, err
			}
		}
		if p.next != "" && p.next != st.Token {
			st.Token = p.next
			moved = true
		}
	}
	return moved, nil
}

type page struct {
	changes []Change
	next    string
}

// readNew reads the changelog from token, or from since without one, to its
// end.
func (a *Adapter) readNew(ctx context.Context, storeID, token string, since time.Time) ([]page, error) {
	var pages []page
	for {
		changes, next, err := a.FGA.Changes(ctx, storeID, "role", token, since, pageSize)
		if err != nil {
			return nil, err
		}
		pages = append(pages, page{changes: changes, next: next})
		if next != "" {
			token = next
		}
		if len(changes) < pageSize {
			return pages, nil
		}
	}
}

const (
	joined = 1
	left   = -1
)

// maxRereads bounds how often joinsAndLeaves reads again because the changelog
// moved while it read the users' roles.
const maxRereads = 5

// unread is the org's unread changelog with the changes that made a user join
// or leave, by change key.
type unread struct {
	pages []page
	joins map[string]int
}

// joinsAndLeaves finds the changes that gave a user their first role in the
// org and those that took their last one, by change key. It reads the users'
// roles now and goes back through the changes, so the answer for a change is
// the same however late or often it is read. Roles are read after the end of
// the changelog; if the changelog moved meanwhile, the new changes are added
// to the pages and their users read again.
func (a *Adapter) joinsAndLeaves(ctx context.Context, st *orgState, pages []page) (unread, error) {
	roles := map[string]map[string]bool{}
	readUsers := func(ps []page) error {
		read := map[string]bool{}
		for _, p := range ps {
			for _, c := range p.changes {
				m, ok := parseMembership(c.TupleKey)
				if !ok || read[m.user] {
					continue
				}
				read[m.user] = true
				tuples, err := a.FGA.ReadUser(ctx, st.StoreID, "user:"+m.user, "role")
				if err != nil {
					return err
				}
				roles[m.user] = map[string]bool{}
				for _, t := range tuples {
					if r, ok := parseMembership(t); ok && r.user == m.user {
						roles[m.user][t.Object] = true
					}
				}
			}
		}
		return nil
	}
	newPages := pages
	for i := 0; ; i++ {
		if err := readUsers(newPages); err != nil {
			return unread{}, err
		}
		if len(roles) == 0 {
			break
		}
		if i == maxRereads {
			return unread{}, fmt.Errorf("the changelog kept changing while reading roles")
		}
		more, err := a.readNew(ctx, st.StoreID, lastToken(pages, st.Token), st.Since)
		if err != nil {
			return unread{}, err
		}
		if countChanges(more) == 0 {
			break
		}
		pages, newPages = append(pages, more...), more
	}

	joins := map[string]int{}
	for i := len(pages) - 1; i >= 0; i-- {
		for j := len(pages[i].changes) - 1; j >= 0; j-- {
			c := pages[i].changes[j]
			m, ok := parseMembership(c.TupleKey)
			if !ok {
				continue
			}
			have := roles[m.user]
			if c.Operation == opWrite {
				if !have[c.TupleKey.Object] {
					a.warnMismatch(st, m.user, c)
				}
				delete(have, c.TupleKey.Object)
				if len(have) == 0 {
					joins[changeKey(c)] = joined
				}
				continue
			}
			if have[c.TupleKey.Object] {
				a.warnMismatch(st, m.user, c)
			}
			if len(have) == 0 || (len(have) == 1 && have[c.TupleKey.Object]) {
				joins[changeKey(c)] = left
			}
			have[c.TupleKey.Object] = true
		}
	}
	return unread{pages: pages, joins: joins}, nil
}

// warnMismatch logs a change that doesn't fit the user's current roles, for
// example a grant of a role they don't have and that no later change revoked.
// Created and deleted for that user may then be wrong.
func (a *Adapter) warnMismatch(st *orgState, user string, c Change) {
	a.Log.Warn().Str("store", st.StoreID).Str("userId", user).Str("object", c.TupleKey.Object).
		Str("operation", c.Operation).Msg("changelog doesn't match the user's roles, user created or deleted may be wrong")
}

func lastToken(pages []page, token string) string {
	for _, p := range pages {
		if p.next != "" {
			token = p.next
		}
	}
	return token
}

func countChanges(pages []page) int {
	n := 0
	for _, p := range pages {
		n += len(p.changes)
	}
	return n
}

// changeKey identifies a change in the changelog.
func changeKey(c Change) string {
	return fmt.Sprintf("openfga/%s/%s/%s/%s/%s", c.Operation, c.TupleKey.User, c.TupleKey.Relation, c.TupleKey.Object, c.Timestamp.Format(time.RFC3339Nano))
}

func (a *Adapter) publishChange(ctx context.Context, org string, m membership, c Change, key string) error {
	path, err := a.accountPath(m, c.Timestamp)
	if err != nil {
		return err
	}
	e, err := contract.MembershipChanged(c.Operation == opWrite, contract.Membership{
		Org:         org,
		Account:     m.account,
		AccountID:   contract.AccountID(m.cluster, m.account),
		AccountPath: path,
		Role:        m.role,
		User:        contract.User{ID: m.user},
	}, key, c.Timestamp)
	if err != nil {
		return err
	}
	return a.publish(ctx, org, e)
}

// publishCreated announces a user who got their first role in the org, with
// email and names by the same rules as a members snapshot.
func (a *Adapter) publishCreated(ctx context.Context, org, user, key string, at time.Time) error {
	m := []contract.Member{{User: contract.User{ID: user}}}
	u := contract.UserCreated{Org: org}
	if !a.addProfiles(ctx, org, m) {
		u.Errors = []contract.SnapshotError{contract.ErrorNamesUnavailable}
	}
	u.User = m[0].User
	e, err := contract.Created(u, key, at)
	if err != nil {
		return err
	}
	return a.publish(ctx, org, e)
}

// publishFirstLogins sends, after a user's first login, the members of every
// account the user has a role on. An invited user is in OpenFGA from the
// invite on, but has names only once they log in, which OpenFGA doesn't see.
func (a *Adapter) publishFirstLogins(ctx context.Context, org string, st *orgState) (bool, error) {
	return a.Logins.EachNew(ctx, org, st.Logins, func(l FirstLogin) error {
		tuples, err := a.FGA.ReadUser(ctx, st.StoreID, "user:"+l.UserID, "role")
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, t := range tuples {
			m, ok := parseMembership(t)
			id := contract.AccountID(m.cluster, m.account)
			if !ok || m.user != l.UserID || seen[id] {
				continue
			}
			seen[id] = true
			if err := a.publishMembers(ctx, org, st.StoreID, m, "keycloak/"+l.ID, l.At); err != nil {
				return err
			}
		}
		return nil
	})
}

// publishMembers sends the account's members as they are now, after the change
// or login named by key at the given time. A deleted account has none left.
func (a *Adapter) publishMembers(ctx context.Context, org, storeID string, m membership, key string, at time.Time) error {
	path, err := a.accountPath(m, at)
	if err != nil {
		return err
	}
	members, err := readMembers(ctx, a.FGA, storeID, m.cluster, m.account)
	if err != nil {
		return err
	}
	var problems []contract.SnapshotError
	if !a.addProfiles(ctx, org, members) {
		problems = append(problems, contract.ErrorNamesUnavailable)
	}
	e, err := contract.MembersSnapshot(contract.AccountMembers{
		Org:         org,
		Account:     m.account,
		AccountID:   contract.AccountID(m.cluster, m.account),
		AccountPath: path,
		Members:     members,
		Errors:      problems,
	}, key, time.Now())
	if err != nil {
		return err
	}
	return a.publish(ctx, org, e)
}

// DefaultProfileTimeout is how long a snapshot waits for all its profiles.
const DefaultProfileTimeout = 15 * time.Second

// addProfiles fills in names and emails. Under the email claim the email is
// the user id, so every member has one, with or without Keycloak. Names, and
// emails under the sub claim, come from Keycloak all or none: a snapshot with
// them for some members only would make a consumer that replaces its copy lose
// what it had. A Keycloak that cannot be reached must not hold membership
// events back either, so within one deadline, and on any error the snapshot
// goes out without them. It returns false if they could not be read, which the
// snapshot reports in its errors. Without a lookup there is nothing to report.
func (a *Adapter) addProfiles(ctx context.Context, org string, members []contract.Member) bool {
	if a.UserClaim == claimEmail {
		for i := range members {
			members[i].User.Email = members[i].User.ID
		}
	}
	if a.Profiles == nil {
		return true
	}
	timeout := a.ProfileTimeout
	if timeout == 0 {
		timeout = DefaultProfileTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	profiles := make([]Profile, len(members))
	for i := range members {
		p, err := a.Profiles.Profile(ctx, org, members[i].User.ID)
		if err != nil {
			a.Log.Warn().Err(err).Str("org", org).Msg("cannot read profiles, publishing without")
			return false
		}
		profiles[i] = p
	}
	for i, p := range profiles {
		members[i].User.FirstName, members[i].User.LastName = p.FirstName, p.LastName
		if a.UserClaim == claimSub {
			members[i].User.Email = p.Email
		}
	}
	return true
}

func (a *Adapter) accountPath(m membership, at time.Time) (string, error) {
	if a.Paths == nil {
		return "", nil
	}
	return a.Paths.Path(m.cluster, m.account, at)
}

func (a *Adapter) publish(ctx context.Context, org string, e cloudevents.Event) error {
	if err := a.Publish(ctx, e); err != nil {
		return err
	}
	a.Log.Info().Str("org", org).Str("type", e.Type()).Str("id", e.ID()).Msg("published")
	return nil
}

func (a *Adapter) loadState(ctx context.Context) (*corev1.ConfigMap, map[string]orgState, error) {
	cm := &corev1.ConfigMap{}
	state := map[string]orgState{}
	err := a.Kube.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: a.Namespace, Name: StateConfigMap}, cm)
	if apierrors.IsNotFound(err) {
		cm.Namespace, cm.Name = a.Namespace, StateConfigMap
		return cm, state, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for org, raw := range cm.Data {
		var st orgState
		if err := json.Unmarshal([]byte(raw), &st); err != nil {
			a.Log.Error().Err(err).Str("org", org).Msg("ignoring broken state")
			continue
		}
		state[org] = st
	}
	return cm, state, nil
}

func (a *Adapter) saveState(ctx context.Context, cm *corev1.ConfigMap, state map[string]orgState) error {
	cm.Data = map[string]string{}
	for org, st := range state {
		b, err := json.Marshal(st)
		if err != nil {
			return err
		}
		cm.Data[org] = string(b)
	}
	if cm.ResourceVersion == "" {
		return a.Kube.Create(ctx, cm)
	}
	return a.Kube.Update(ctx, cm)
}
