/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import "strings"

// accountType is the OpenFGA type Platform Mesh uses for accounts. Role objects
// of other types (for example roles on APIExports) are not memberships.
const accountType = "core_platform-mesh_io_account"

// membership is what a role assignment tuple says, in Platform Mesh terms.
type membership struct {
	cluster string
	account string
	role    string
	// user is the user as the tuple has it: the value of the configured user
	// claim.
	user string
}

// parseMembership reads a tuple like
//
//	user:alice@example.com  assignee  role:core_platform-mesh_io_account/<cluster>/<account>/<role>
//
// which is how iam-service and security-operator record that a user has a
// role on an account. The cluster is the workspace that holds the Account
// object.
func parseMembership(k TupleKey) (membership, bool) {
	if k.Relation != "assignee" || !strings.HasPrefix(k.User, "user:") || k.User == "user:" || k.User == "user:*" {
		return membership{}, false
	}
	parts := strings.Split(strings.TrimPrefix(k.Object, "role:"), "/")
	if !strings.HasPrefix(k.Object, "role:") || len(parts) != 4 || parts[0] != accountType || parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return membership{}, false
	}
	return membership{cluster: parts[1], account: parts[2], role: parts[3], user: strings.TrimPrefix(k.User, "user:")}, true
}
