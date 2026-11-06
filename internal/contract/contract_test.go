/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package contract

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMembershipChanged(t *testing.T) {
	m := Membership{Org: "acme", Account: "team-a", AccountID: AccountID("c1", "team-a"), Role: "member", User: User{ID: "alice@example.com"}}
	e, err := MembershipChanged(true, m, "change-1", time.Unix(0, 0))
	require.NoError(t, err)
	require.NoError(t, e.Validate())

	assert.Equal(t, TypeMembershipGranted, e.Type())
	assert.Equal(t, "platform-mesh.io/orgs/acme/iam", e.Source())
	assert.Equal(t, "accounts/c1/team-a", e.Subject())
	assert.Equal(t, "acme", e.Extensions()[OrgExtension])
	assert.JSONEq(t, `{"org":"acme","account":"team-a","accountId":"c1/team-a","role":"member","user":{"id":"alice@example.com"}}`, string(e.Data()))

	m.AccountPath = "root:orgs:acme:team-a"
	withPath, _ := MembershipChanged(true, m, "change-1", time.Unix(0, 0))
	assert.Contains(t, string(withPath.Data()), `"accountPath":"root:orgs:acme:team-a"`)
	m.AccountPath = ""

	again, _ := MembershipChanged(true, m, "change-1", time.Unix(0, 0))
	other, _ := MembershipChanged(true, Membership{Org: "other"}, "change-1", time.Unix(0, 0))
	assert.Equal(t, e.ID(), again.ID(), "same change, same id")
	assert.NotEqual(t, e.ID(), other.ID(), "ids differ between orgs")

	revoked, _ := MembershipChanged(false, m, "change-2", time.Unix(0, 0))
	assert.Equal(t, TypeMembershipRevoked, revoked.Type())
}

func TestMembersSnapshot(t *testing.T) {
	m := AccountMembers{Org: "acme", Account: "team-a", AccountID: AccountID("c1", "team-a"), Members: []Member{
		{User: User{ID: "alice@example.com"}, Roles: []string{"member", "owner"}},
	}}
	e, err := MembersSnapshot(m, "change-1", time.Unix(0, 0))
	require.NoError(t, err)
	require.NoError(t, e.Validate())

	assert.Equal(t, TypeAccountMembers, e.Type())
	assert.Equal(t, "platform-mesh.io/orgs/acme/iam", e.Source())
	assert.Equal(t, "accounts/c1/team-a", e.Subject())
	assert.Equal(t, "acme", e.Extensions()[OrgExtension])
	assert.JSONEq(t, `{"org":"acme","account":"team-a","accountId":"c1/team-a","members":[{"user":{"id":"alice@example.com"},"roles":["member","owner"]}]}`, string(e.Data()))

	change, _ := MembershipChanged(true, Membership{Org: "acme"}, "change-1", time.Unix(0, 0))
	assert.NotEqual(t, change.ID(), e.ID(), "a snapshot and the change it follows have different ids")
	again, _ := MembersSnapshot(m, "change-1", time.Unix(1, 0))
	assert.Equal(t, e.ID(), again.ID(), "same last change and list, same id")
	m.Members = append(m.Members, Member{User: User{ID: "bob@example.com"}, Roles: []string{"member"}})
	changed, _ := MembersSnapshot(m, "change-1", time.Unix(1, 0))
	assert.NotEqual(t, e.ID(), changed.ID(), "a retry that reads another list is another event")

	empty, err := MembersSnapshot(AccountMembers{Org: "acme"}, "change-2", time.Unix(0, 0))
	require.NoError(t, err)
	assert.Contains(t, string(empty.Data()), `"members":[]`)
}
