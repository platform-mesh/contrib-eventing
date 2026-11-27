/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package keycloak

import (
	"context"
	"slices"
)

const pageSize = 50

// Cursor is how far a reader got in a realm's stored events.
type Cursor struct {
	// Since is the time of the last event read, in epoch milliseconds. Keycloak
	// includes events at exactly that time in the next read. Zero is the
	// beginning of the realm's events.
	Since int64 `json:"since,omitempty"`
	// Seen are the ids of the events at Since that were handled already.
	Seen []string `json:"seen,omitempty"`
}

// EachNew hands the realm's new events of one type to handle, oldest first,
// page by page. The cursor only moves after every event of a page was
// handled; if one fails, the whole page is read again next time. It returns
// whether the cursor moved.
func (c *Client) EachNew(ctx context.Context, realm, typ string, cur *Cursor, handle func(Event) error) (bool, error) {
	moved := false
	for {
		want := pageSize + len(cur.Seen)
		events, err := c.Events(ctx, realm, typ, cur.Since, want)
		if err != nil {
			return moved, err
		}
		next := Cursor{Since: cur.Since, Seen: slices.Clone(cur.Seen)}
		for _, ev := range events {
			if ev.Time < next.Since || (ev.Time == next.Since && slices.Contains(next.Seen, ev.ID)) {
				continue
			}
			if err := handle(ev); err != nil {
				return moved, err
			}
			if ev.Time > next.Since {
				next.Since, next.Seen = ev.Time, nil
			}
			next.Seen = append(next.Seen, ev.ID)
		}
		if next.Since != cur.Since || len(next.Seen) != len(cur.Seen) {
			*cur = next
			moved = true
		}
		if len(events) < want {
			return moved, nil
		}
	}
}
