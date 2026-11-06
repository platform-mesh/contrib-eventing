/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package knative

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-mesh/contrib-eventing/api/v1alpha1"
	"github.com/platform-mesh/contrib-eventing/internal/delivery"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestTriggerSpec(t *testing.T) {
	retry := int32(3)
	sub := &v1alpha1.Subscription{Spec: v1alpha1.SubscriptionSpec{
		Filter:   v1alpha1.Filter{Types: []string{"a", "b"}},
		Sink:     v1alpha1.Sink{URI: "http://sink"},
		Delivery: &v1alpha1.Delivery{Retry: &retry},
	}}
	assert.Equal(t, map[string]any{
		"broker": "default",
		"filters": []any{map[string]any{"any": []any{
			map[string]any{"exact": map[string]any{"type": "a"}},
			map[string]any{"exact": map[string]any{"type": "b"}},
		}}},
		"subscriber": map[string]any{"uri": "http://sink"},
		"delivery": map[string]any{
			"retry":         int64(3),
			"backoffPolicy": "exponential",
			"backoffDelay":  "PT0.2S",
		},
	}, triggerSpec(sub))
}

func TestTriggerSpecBackoff(t *testing.T) {
	policy, delay := "linear", "PT1S"
	sub := &v1alpha1.Subscription{Spec: v1alpha1.SubscriptionSpec{
		Filter: v1alpha1.Filter{Types: []string{"a"}},
		Sink:   v1alpha1.Sink{URI: "http://sink"},
	}}
	// No delivery: the Trigger takes the Broker's.
	assert.NotContains(t, triggerSpec(sub), "delivery")

	// A delivery always has a backoff, the Subscription's or the default.
	sub.Spec.Delivery = &v1alpha1.Delivery{}
	assert.Equal(t, map[string]any{"backoffPolicy": "exponential", "backoffDelay": "PT0.2S"}, triggerSpec(sub)["delivery"])
	sub.Spec.Delivery = &v1alpha1.Delivery{BackoffPolicy: &policy, BackoffDelay: &delay}
	assert.Equal(t, map[string]any{"backoffPolicy": "linear", "backoffDelay": "PT1S"}, triggerSpec(sub)["delivery"])
}

func TestTriggerSpecDeadLetters(t *testing.T) {
	timeout := "PT10S"
	sub := &v1alpha1.Subscription{Spec: v1alpha1.SubscriptionSpec{
		Filter: v1alpha1.Filter{Types: []string{"a"}},
		Sink:   v1alpha1.Sink{URI: "http://sink"},
		Delivery: &v1alpha1.Delivery{
			Timeout:        &timeout,
			DeadLetterSink: &v1alpha1.Sink{URI: "http://dead-letters"},
		},
	}}
	assert.Equal(t, map[string]any{
		"backoffPolicy":  "exponential",
		"backoffDelay":   "PT0.2S",
		"timeout":        "PT10S",
		"deadLetterSink": map[string]any{"uri": "http://dead-letters"},
	}, triggerSpec(sub)["delivery"])
}

func TestEnsureBroker(t *testing.T) {
	ctx := context.Background()
	b := &Backend{Client: fake.NewClientBuilder().Build(), Namespace: "contrib-eventing"}

	require.NoError(t, b.ensureBroker(ctx))
	// A second call finds the Broker and leaves it alone.
	require.NoError(t, b.ensureBroker(ctx))
	br := &unstructured.Unstructured{}
	br.SetGroupVersionKind(brokerGVK)
	require.NoError(t, b.Client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "contrib-eventing", Name: BrokerName}, br))
}

func TestEnsureRejected(t *testing.T) {
	rejected := apierrors.NewBadRequest(`admission webhook "validation.webhook.eventing.knative.dev" denied the request: validation failed: invalid value: abc: spec.delivery.timeout`)
	c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c ctrlruntimeclient.WithWatch, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
			if obj.GetObjectKind().GroupVersionKind() == triggerGVK {
				return rejected
			}
			return c.Create(ctx, obj, opts...)
		},
	}).Build()
	b := &Backend{Client: c, Namespace: "contrib-eventing"}
	timeout := "abc"
	sub := &v1alpha1.Subscription{Spec: v1alpha1.SubscriptionSpec{
		Filter:   v1alpha1.Filter{Types: []string{"a"}},
		Sink:     v1alpha1.Sink{URI: "http://sink"},
		Delivery: &v1alpha1.Delivery{Timeout: &timeout},
	}}

	_, err := b.Ensure(context.Background(), "t", sub)
	require.ErrorIs(t, err, delivery.ErrInvalid)
	assert.Contains(t, err.Error(), "spec.delivery.timeout")
}

func TestTriggerSpecWithOrgs(t *testing.T) {
	sub := &v1alpha1.Subscription{Spec: v1alpha1.SubscriptionSpec{
		Filter: v1alpha1.Filter{Types: []string{"a"}, Orgs: []string{"acme"}},
		Sink:   v1alpha1.Sink{URI: "http://sink"},
	}}
	assert.Equal(t, []any{
		map[string]any{"any": []any{map[string]any{"exact": map[string]any{"type": "a"}}}},
		map[string]any{"any": []any{map[string]any{"exact": map[string]any{"pmorg": "acme"}}}},
	}, triggerSpec(sub)["filters"])
}
