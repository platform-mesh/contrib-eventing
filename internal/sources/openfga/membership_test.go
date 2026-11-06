/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseMembership(t *testing.T) {
	change := func(user, relation, object string) TupleKey {
		return TupleKey{User: user, Relation: relation, Object: object}
	}

	got, ok := parseMembership(change("user:alice@example.com", "assignee", "role:core_platform-mesh_io_account/32epcrhea5sv5v5b/team-a/member"))
	assert.True(t, ok)
	assert.Equal(t, membership{cluster: "32epcrhea5sv5v5b", account: "team-a", role: "member", user: "alice@example.com"}, got)

	// The user is whatever the configured user claim gives, not always an email.
	got, ok = parseMembership(change("user:kcp-admin", "assignee", "role:core_platform-mesh_io_account/32epcrhea5sv5v5b/team-a/owner"))
	assert.True(t, ok)
	assert.Equal(t, "kcp-admin", got.user)

	for _, k := range []TupleKey{
		// The second tuple iam-service writes: the role on the account.
		change("role:core_platform-mesh_io_account/32epcrhea5sv5v5b/team-a/member#assignee", "member", "core_platform-mesh_io_account:32epcrhea5sv5v5b/team-a"),
		change("user:*", "assignee", "role:authenticated"),
		change("user:", "assignee", "role:core_platform-mesh_io_account/abc/team-a/member"),
		change("user:alice@example.com", "assignee", "role:apis_kcp_io_apiexport/abc/export/owner"),
		change("user:alice@example.com", "assignee", "role:core_platform-mesh_io_account/abc/team-a"),
		change("user:alice@example.com", "assignee", "role:core_platform-mesh_io_account//team-a/member"),
	} {
		_, ok := parseMembership(k)
		assert.False(t, ok, "%+v is not a membership", k)
	}
}
