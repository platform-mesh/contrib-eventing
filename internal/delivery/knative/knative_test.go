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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
		"delivery":   map[string]any{"retry": int64(3)},
	}, triggerSpec(sub))
}

func TestEnsureBrokerIsAsync(t *testing.T) {
	ctx := context.Background()
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(brokerGVK)
	existing.SetNamespace("contrib-eventing")
	existing.SetName(BrokerName)
	existing.SetAnnotations(map[string]string{"eventing.knative.dev/broker.class": "MTChannelBasedBroker"})
	b := &Backend{Client: fake.NewClientBuilder().WithObjects(existing).Build(), Namespace: "contrib-eventing"}

	// A Broker from an older install gets the annotation and keeps the others.
	require.NoError(t, b.ensureBroker(ctx))
	br := &unstructured.Unstructured{}
	br.SetGroupVersionKind(brokerGVK)
	require.NoError(t, b.Client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "contrib-eventing", Name: BrokerName}, br))
	assert.Equal(t, map[string]string{
		"eventing.knative.dev/broker.class": "MTChannelBasedBroker",
		asyncHandlerAnnotation:              "true",
	}, br.GetAnnotations())
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
