/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package knative delivers events with Knative Eventing: one Broker for the
// platform, one Trigger per Subscription. Knative objects are handled as
// unstructured so we don't depend on Knative's Go modules.
package knative

import (
	"context"
	"fmt"

	"github.com/platform-mesh/contrib-eventing/api/v1alpha1"
	"github.com/platform-mesh/contrib-eventing/internal/contract"
	"github.com/platform-mesh/contrib-eventing/internal/delivery"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// BrokerName is the name of the one Broker.
	BrokerName = "default"

	// asyncHandlerAnnotation makes the Broker's channel accept an event before
	// it is delivered. Without it the channel waits for every Trigger, retries
	// included, so one failing sink holds up the ingress and the adapter
	// publishes the event again to everyone. Knative copies the annotation from
	// the Broker to the channel.
	asyncHandlerAnnotation = "messaging.knative.dev/async-handler"
)

var (
	brokerGVK  = schema.GroupVersionKind{Group: "eventing.knative.dev", Version: "v1", Kind: "Broker"}
	triggerGVK = schema.GroupVersionKind{Group: "eventing.knative.dev", Version: "v1", Kind: "Trigger"}
)

// Backend implements delivery.Backend with one Broker for the whole platform.
type Backend struct {
	Client ctrlruntimeclient.Client
	// Namespace holds the Broker and the Triggers.
	Namespace string
}

// Ensure makes sure the Broker exists and the Subscription has a Trigger, and
// reports whether the Trigger is ready.
func (b *Backend) Ensure(ctx context.Context, trigger string, sub *v1alpha1.Subscription) (delivery.Status, error) {
	if err := b.ensureBroker(ctx); err != nil {
		return delivery.Status{}, err
	}

	t := &unstructured.Unstructured{}
	t.SetGroupVersionKind(triggerGVK)
	t.SetNamespace(b.Namespace)
	t.SetName(trigger)
	_, err := controllerutil.CreateOrUpdate(ctx, b.Client, t, func() error {
		t.SetAnnotations(map[string]string{"eventing.platform-mesh.io/subscription": sub.Name})
		return unstructured.SetNestedMap(t.Object, triggerSpec(sub), "spec")
	})
	if err != nil {
		return delivery.Status{}, fmt.Errorf("trigger %s/%s: %w", b.Namespace, trigger, err)
	}
	return readiness(t), nil
}

// Delete removes the Subscription's Trigger. The Broker stays.
func (b *Backend) Delete(ctx context.Context, trigger string) error {
	t := &unstructured.Unstructured{}
	t.SetGroupVersionKind(triggerGVK)
	t.SetNamespace(b.Namespace)
	t.SetName(trigger)
	return ctrlruntimeclient.IgnoreNotFound(b.Client.Delete(ctx, t))
}

func (b *Backend) ensureBroker(ctx context.Context) error {
	br := &unstructured.Unstructured{}
	br.SetGroupVersionKind(brokerGVK)
	br.SetNamespace(b.Namespace)
	br.SetName(BrokerName)
	_, err := controllerutil.CreateOrUpdate(ctx, b.Client, br, func() error {
		annotations := br.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[asyncHandlerAnnotation] = "true"
		br.SetAnnotations(annotations)
		return nil
	})
	if err != nil {
		return fmt.Errorf("broker %s/%s: %w", b.Namespace, BrokerName, err)
	}
	return nil
}

// triggerSpec translates a Subscription into a Trigger spec. Knative delivers
// an event only if every filter matches: one for the types, one for the orgs.
func triggerSpec(sub *v1alpha1.Subscription) map[string]any {
	filters := []any{anyOf("type", sub.Spec.Filter.Types)}
	if len(sub.Spec.Filter.Orgs) > 0 {
		filters = append(filters, anyOf(contract.OrgExtension, sub.Spec.Filter.Orgs))
	}
	spec := map[string]any{
		"broker":     BrokerName,
		"filters":    filters,
		"subscriber": map[string]any{"uri": sub.Spec.Sink.URI},
	}
	if d := sub.Spec.Delivery; d != nil {
		delivery := map[string]any{}
		if d.Retry != nil {
			delivery["retry"] = int64(*d.Retry)
		}
		if d.BackoffPolicy != nil {
			delivery["backoffPolicy"] = *d.BackoffPolicy
		}
		if d.BackoffDelay != nil {
			delivery["backoffDelay"] = *d.BackoffDelay
		}
		spec["delivery"] = delivery
	}
	return spec
}

// anyOf matches events whose attribute has one of the values.
func anyOf(attribute string, values []string) map[string]any {
	exact := make([]any, 0, len(values))
	for _, v := range values {
		exact = append(exact, map[string]any{"exact": map[string]any{attribute: v}})
	}
	return map[string]any{"any": exact}
}

// readiness reads the Trigger's Ready condition. Knative folds the Broker's
// state into it, so this is the only thing we need to look at.
func readiness(t *unstructured.Unstructured) delivery.Status {
	conditions, _, _ := unstructured.NestedSlice(t.Object, "status", "conditions")
	for _, c := range conditions {
		m, ok := c.(map[string]any)
		if !ok || m["type"] != "Ready" {
			continue
		}
		reason, _ := m["reason"].(string)
		message, _ := m["message"].(string)
		if m["status"] == "True" {
			return delivery.Status{Ready: true, Reason: "Ready", Message: "events are delivered to the sink"}
		}
		if reason == "" {
			reason = "TriggerNotReady"
		}
		return delivery.Status{Reason: reason, Message: message}
	}
	return delivery.Status{Reason: "TriggerNotReady", Message: "waiting for Knative to set up delivery"}
}
