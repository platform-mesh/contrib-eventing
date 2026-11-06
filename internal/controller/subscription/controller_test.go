/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package subscription

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-mesh/contrib-eventing/api/v1alpha1"
	"github.com/platform-mesh/contrib-eventing/internal/delivery"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeBackend struct {
	ensured, deleted []string
	err              error
}

func (f *fakeBackend) Ensure(_ context.Context, name string, _ *v1alpha1.Subscription) (delivery.Status, error) {
	f.ensured = append(f.ensured, name)
	if f.err != nil {
		return delivery.Status{}, f.err
	}
	return delivery.Status{Ready: true, Reason: "Ready"}, nil
}

func (f *fakeBackend) Delete(_ context.Context, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}

func workspace(t *testing.T, objs ...ctrlruntimeclient.Object) ctrlruntimeclient.Client {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.Subscription{}).
		Build()
}

func subscription() *v1alpha1.Subscription {
	return &v1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Spec:       v1alpha1.SubscriptionSpec{Sink: v1alpha1.Sink{URI: "http://sink.consumer.svc.cluster.local"}},
	}
}

func TestReconcile(t *testing.T) {
	backend := &fakeBackend{}
	c := workspace(t, subscription())
	r := &Reconciler{Delivery: backend, AllowedSinkHosts: []string{"*.consumer.svc.cluster.local"}}

	_, err := r.reconcile(context.Background(), c, "c1", types.NamespacedName{Name: "s"})
	require.NoError(t, err)

	got := &v1alpha1.Subscription{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "s"}, got))
	assert.Equal(t, metav1.ConditionTrue, got.Status.Conditions[0].Status)
	assert.Contains(t, got.Finalizers, finalizer)
	assert.Equal(t, []string{deliveryName("c1", "s")}, backend.ensured)
}

func TestDelete(t *testing.T) {
	backend := &fakeBackend{}
	sub := subscription()
	sub.Finalizers = []string{finalizer}
	sub.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	c := workspace(t, sub)
	r := &Reconciler{Delivery: backend}

	_, err := r.reconcile(context.Background(), c, "c1", types.NamespacedName{Name: "s"})
	require.NoError(t, err)
	assert.Equal(t, []string{deliveryName("c1", "s")}, backend.deleted)
}

func TestSinkNotAllowed(t *testing.T) {
	for name, allowed := range map[string][]string{
		"empty allowlist": nil,
		"other host":      {"sink.other.svc.cluster.local"},
	} {
		t.Run(name, func(t *testing.T) {
			backend := &fakeBackend{}
			c := workspace(t, subscription())
			r := &Reconciler{Delivery: backend, AllowedSinkHosts: allowed}

			_, err := r.reconcile(context.Background(), c, "c1", types.NamespacedName{Name: "s"})
			require.NoError(t, err)

			got := &v1alpha1.Subscription{}
			require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "s"}, got))
			assert.Equal(t, metav1.ConditionFalse, got.Status.Conditions[0].Status)
			assert.Equal(t, "SinkNotAllowed", got.Status.Conditions[0].Reason)
			assert.Empty(t, backend.ensured)
			assert.Equal(t, []string{deliveryName("c1", "s")}, backend.deleted, "an existing Trigger must go")
		})
	}
}

func TestInvalidDelivery(t *testing.T) {
	backend := &fakeBackend{err: fmt.Errorf("%w: timeout abc", delivery.ErrInvalid)}
	c := workspace(t, subscription())
	r := &Reconciler{Delivery: backend, AllowedSinkHosts: []string{"*.svc.cluster.local"}}

	res, err := r.reconcile(context.Background(), c, "c1", types.NamespacedName{Name: "s"})
	require.NoError(t, err, "trying again doesn't help")
	assert.Zero(t, res.RequeueAfter)

	got := &v1alpha1.Subscription{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "s"}, got))
	assert.Equal(t, metav1.ConditionFalse, got.Status.Conditions[0].Status)
	assert.Equal(t, "InvalidDelivery", got.Status.Conditions[0].Reason)
	assert.Contains(t, got.Status.Conditions[0].Message, "timeout abc")
	assert.Equal(t, []string{deliveryName("c1", "s")}, backend.deleted, "the Trigger from before must go")
}

func TestCheckSinks(t *testing.T) {
	allowed := []string{"event-display.demo.svc.cluster.local", "*.consumer.svc.cluster.local"}
	sub := func(sink, deadLetters string) *v1alpha1.Subscription {
		s := &v1alpha1.Subscription{Spec: v1alpha1.SubscriptionSpec{Sink: v1alpha1.Sink{URI: sink}}}
		if deadLetters != "" {
			s.Spec.Delivery = &v1alpha1.Delivery{DeadLetterSink: &v1alpha1.Sink{URI: deadLetters}}
		}
		return s
	}
	for _, ok := range []*v1alpha1.Subscription{
		sub("http://event-display.demo.svc.cluster.local", ""),
		sub("https://EVENT-DISPLAY.demo.svc.cluster.local:8443/path", ""),
		sub("http://a.consumer.svc.cluster.local", "http://b.consumer.svc.cluster.local"),
		sub("http://a.b.consumer.svc.cluster.local", ""),
	} {
		assert.NoError(t, checkSinks(ok, allowed), ok.Spec.Sink.URI)
	}
	for _, bad := range []*v1alpha1.Subscription{
		sub("http://example.com", ""),
		sub("http://consumer.svc.cluster.local", ""),
		sub("http://evilconsumer.svc.cluster.local", ""),
		sub("http://event-display.demo.svc.cluster.local.example.com", ""),
		sub("http://a.consumer.svc.cluster.local", "http://example.com"),
		sub("http://", ""),
		// The host is what the request goes to, not what the URL starts with.
		sub("http://event-display.demo.svc.cluster.local@example.com", ""),
		sub("http://example.com#@event-display.demo.svc.cluster.local", ""),
		sub("http://example.com?@event-display.demo.svc.cluster.local", ""),
		sub(`http://example.com\@event-display.demo.svc.cluster.local`, ""),
		sub("http://event%2Ddisplay.demo.svc.cluster.local", ""),
		sub("http://10.96.0.1", ""),
		sub("http://[::1]:80", ""),
		// Strict: a trailing dot names the same host but isn't on the list.
		sub("http://event-display.demo.svc.cluster.local.", ""),
	} {
		assert.Error(t, checkSinks(bad, allowed), bad.Spec.Sink.URI)
	}
}
