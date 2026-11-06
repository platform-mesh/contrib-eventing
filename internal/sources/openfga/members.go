/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"context"
	"slices"
	"strings"

	"github.com/platform-mesh/contrib-eventing/internal/contract"
)

// readMembers returns who has which role on an account, sorted by user. It
// follows the tuples iam-service writes: the account names its roles
//
//	role:core_platform-mesh_io_account/<cluster>/<account>/<role>#assignee  <role>  core_platform-mesh_io_account:<cluster>/<account>
//
// and each role names its users. security-operator finds them the same way
// when it cleans up a deleted account.
func readMembers(ctx context.Context, fga *Client, storeID, cluster, account string) ([]contract.Member, error) {
	onAccount, err := fga.Read(ctx, storeID, accountType+":"+cluster+"/"+account)
	if err != nil {
		return nil, err
	}
	rolePrefix := "role:" + accountType + "/" + cluster + "/" + account + "/"
	roles := map[string][]string{}
	for _, t := range onAccount {
		// Other tuples on the account, like its parent, are not roles.
		role, ok := strings.CutSuffix(t.User, "#assignee")
		if !ok || !strings.HasPrefix(role, rolePrefix) {
			continue
		}
		assignees, err := fga.Read(ctx, storeID, role)
		if err != nil {
			return nil, err
		}
		for _, a := range assignees {
			m, ok := parseMembership(a)
			if !ok || m.cluster != cluster || m.account != account {
				continue
			}
			if !slices.Contains(roles[m.user], m.role) {
				roles[m.user] = append(roles[m.user], m.role)
			}
		}
	}

	members := make([]contract.Member, 0, len(roles))
	for user, r := range roles {
		slices.Sort(r)
		members = append(members, contract.Member{User: contract.User{ID: user}, Roles: r})
	}
	slices.SortFunc(members, func(a, b contract.Member) int { return strings.Compare(a.User.ID, b.User.ID) })
	return members, nil
}
