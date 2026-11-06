/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package subscription

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/platform-mesh/contrib-eventing/api/v1alpha1"
)

// checkSinks returns an error if the sink or the dead letter sink of the
// Subscription is not on the allowlist. Events carry user ids, so the platform
// operator decides where they may go.
func checkSinks(sub *v1alpha1.Subscription, allowed []string) error {
	uris := []string{sub.Spec.Sink.URI}
	if d := sub.Spec.Delivery; d != nil && d.DeadLetterSink != nil {
		uris = append(uris, d.DeadLetterSink.URI)
	}
	for _, uri := range uris {
		u, err := url.Parse(uri)
		if err != nil {
			return fmt.Errorf("sink %q is not a URL", uri)
		}
		if !hostAllowed(u.Hostname(), allowed) {
			return fmt.Errorf("host %q of sink %s is not allowed by the platform operator", u.Hostname(), uri)
		}
	}
	return nil
}

// hostAllowed matches a host against entries that are either a host name or
// *.suffix for every host below suffix. No entries allow nothing.
func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(host)
	if host == "" {
		return false
	}
	for _, a := range allowed {
		a = strings.ToLower(a)
		if suffix, ok := strings.CutPrefix(a, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if host == a {
			return true
		}
	}
	return false
}
