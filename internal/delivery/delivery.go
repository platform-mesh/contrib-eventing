/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package delivery is the seam between Subscriptions and whatever moves the
// events. Knative is the only backend for now.
package delivery

import (
	"context"
	"errors"

	"github.com/platform-mesh/contrib-eventing/api/v1alpha1"
)

// ErrInvalid wraps errors of a backend that rejects the Subscription's
// delivery settings. Trying again doesn't help; the Subscription has to change.
var ErrInvalid = errors.New("invalid delivery")

// Backend delivers events to the sinks of the Subscriptions.
type Backend interface {
	// Ensure sets up delivery for a Subscription. name is unique per
	// Subscription across all workspaces.
	Ensure(ctx context.Context, name string, sub *v1alpha1.Subscription) (Status, error)
	// Delete stops delivery for a Subscription.
	Delete(ctx context.Context, name string) error
}

// Status is the delivery state of one Subscription.
type Status struct {
	Ready   bool
	Reason  string
	Message string
}
