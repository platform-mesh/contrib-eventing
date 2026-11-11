/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package contract builds the CloudEvents described in docs/contract.md. Every
// source goes through here, so all events share the same envelope rules.
package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
)

const (
	TypeMembershipGranted = "io.platform-mesh.iam.membership.granted.v1alpha1"
	TypeMembershipRevoked = "io.platform-mesh.iam.membership.revoked.v1alpha1"
	TypeAccountMembers    = "io.platform-mesh.iam.account.members.v1alpha1"
	TypeUserLoggedIn      = "io.platform-mesh.iam.user.loggedin.v1alpha1"

	// OrgExtension carries the org name, so subscribers can filter without
	// reading the payload.
	OrgExtension = "pmorg"
)

// User identifies a person the way Platform Mesh does: by the value of the
// user claim the platform admin configured, email by default.
type User struct {
	ID string `json:"id"`
	// FirstName and LastName are set where Keycloak knows them. Only the
	// member snapshots carry them.
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
}

// UserLoggedIn is a login in one of the org's realms. It has no IP address,
// session or client.
type UserLoggedIn struct {
	Org  string `json:"org"`
	User User   `json:"user"`
}

type Membership struct {
	Org     string `json:"org"`
	Account string `json:"account"`
	// AccountID is unique in the platform, unlike the name of a nested account.
	AccountID string `json:"accountId"`
	// AccountPath is the account's kcp workspace path. Empty if the workspace
	// was gone before the event was built.
	AccountPath string `json:"accountPath,omitempty"`
	Role        string `json:"role"`
	User        User   `json:"user"`
}

// Member is a user and every role they have on an account.
type Member struct {
	User  User     `json:"user"`
	Roles []string `json:"roles"`
}

// AccountMembers is the full member list of an account.
type AccountMembers struct {
	Org         string   `json:"org"`
	Account     string   `json:"account"`
	AccountID   string   `json:"accountId"`
	AccountPath string   `json:"accountPath,omitempty"`
	Members     []Member `json:"members"`
	// Errors lists what went wrong while the snapshot was built, as codes from
	// the contract. Empty when nothing did.
	Errors []string `json:"errors,omitempty"`
}

// ErrorNamesUnavailable means the names could not be read from Keycloak, so the
// snapshot has none, though some members may have them.
const ErrorNamesUnavailable = "namesUnavailable"

// AccountID is the logical cluster of the workspace that holds the Account
// object and the account's name, the same pair AccountInfo shows as
// originClusterId and name.
func AccountID(cluster, name string) string {
	return cluster + "/" + name
}

// MembershipChanged builds a granted or revoked event. changeKey identifies the
// change at the source; the same change always gives the same event id.
func MembershipChanged(granted bool, m Membership, changeKey string, at time.Time) (cloudevents.Event, error) {
	typ := TypeMembershipRevoked
	if granted {
		typ = TypeMembershipGranted
	}
	return newEvent(typ, m.Org, "accounts/"+m.AccountID, id(m.Org, changeKey), at, m)
}

// MembersSnapshot builds a members event. at is when the list was read. The id
// comes from changeKey, the last change that led to it, and from the list: a
// retry gives the same id, unless the list changed in between, because then it
// is another event.
func MembersSnapshot(m AccountMembers, changeKey string, at time.Time) (cloudevents.Event, error) {
	if m.Members == nil {
		// An account without members has an empty list, not null.
		m.Members = []Member{}
	}
	data, err := json.Marshal(m)
	if err != nil {
		return cloudevents.Event{}, err
	}
	return newEvent(TypeAccountMembers, m.Org, "accounts/"+m.AccountID, id(m.Org, TypeAccountMembers, changeKey, string(data)), at, m)
}

// LoggedIn builds a login event. keycloakEventID is the id of the stored
// Keycloak event, so a repeat gives the same event id. at is the login time.
func LoggedIn(org string, user User, keycloakEventID string, at time.Time) (cloudevents.Event, error) {
	return newEvent(TypeUserLoggedIn, org, "users/"+user.ID, id(org, "keycloak", keycloakEventID), at, UserLoggedIn{Org: org, User: user})
}

func newEvent(typ, org, subject, eventID string, at time.Time, data any) (cloudevents.Event, error) {
	e := cloudevents.NewEvent()
	e.SetID(eventID)
	e.SetType(typ)
	e.SetSource("platform-mesh.io/orgs/" + org + "/iam")
	e.SetSubject(subject)
	e.SetTime(at)
	e.SetExtension(OrgExtension, org)
	err := e.SetData(cloudevents.ApplicationJSON, data)
	return e, err
}

func id(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "/")))
	return hex.EncodeToString(sum[:16])
}
