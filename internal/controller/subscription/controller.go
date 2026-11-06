/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package subscription reconciles the platform admins' Subscriptions into the
// delivery backend.
package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/platform-mesh/contrib-eventing/api/v1alpha1"
	"github.com/platform-mesh/contrib-eventing/internal/delivery"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

const finalizer = "eventing.platform-mesh.io/delivery"

type Reconciler struct {
	Manager  mcmanager.Manager
	Delivery delivery.Backend
	// AllowedSinkHosts are the hosts sinks may point at: a host name, or
	// *.suffix for every host below suffix.
	AllowedSinkHosts []string
}

func (r *Reconciler) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		Named("subscription").
		For(&v1alpha1.Subscription{}).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.Manager.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	return r.reconcile(ctx, cl.GetClient(), string(req.ClusterName), req.NamespacedName)
}

// reconcile works on one workspace, through a client for that workspace. Only
// platform admins can bind the API, so every workspace that has it is theirs.
func (r *Reconciler) reconcile(ctx context.Context, c ctrlruntimeclient.Client, cluster string, key types.NamespacedName) (ctrl.Result, error) {
	sub := &v1alpha1.Subscription{}
	if err := c.Get(ctx, key, sub); err != nil {
		return ctrl.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}
	name := deliveryName(cluster, sub.Name)

	if !sub.DeletionTimestamp.IsZero() {
		if err := r.Delivery.Delete(ctx, name); err != nil {
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(sub, finalizer)
		return ctrl.Result{}, ctrlruntimeclient.IgnoreNotFound(c.Update(ctx, sub))
	}
	if controllerutil.AddFinalizer(sub, finalizer) {
		if err := c.Update(ctx, sub); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := checkSinks(sub, r.AllowedSinkHosts); err != nil {
		// Stop delivery that was set up before the sink changed.
		if err := r.Delivery.Delete(ctx, name); err != nil {
			return ctrl.Result{}, err
		}
		return setStatus(ctx, c, sub, delivery.Status{Reason: "SinkNotAllowed", Message: err.Error()}, 0)
	}

	status, err := r.Delivery.Ensure(ctx, name, sub)
	if errors.Is(err, delivery.ErrInvalid) {
		// Don't keep delivering with the settings from before.
		if err := r.Delivery.Delete(ctx, name); err != nil {
			return ctrl.Result{}, err
		}
		return setStatus(ctx, c, sub, delivery.Status{Reason: "InvalidDelivery", Message: err.Error()}, 0)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	// We don't watch the backend, so check again until delivery is ready.
	requeue := time.Duration(0)
	if !status.Ready {
		requeue = 5 * time.Second
	}
	return setStatus(ctx, c, sub, status, requeue)
}

func setStatus(ctx context.Context, c ctrlruntimeclient.Client, sub *v1alpha1.Subscription, s delivery.Status, requeue time.Duration) (ctrl.Result, error) {
	cond := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             s.Reason,
		Message:            s.Message,
		ObservedGeneration: sub.Generation,
	}
	if s.Ready {
		cond.Status = metav1.ConditionTrue
	}
	sub.Status.ObservedGeneration = sub.Generation
	meta.SetStatusCondition(&sub.Status.Conditions, cond)
	if err := c.Status().Update(ctx, sub); err != nil {
		return ctrl.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// deliveryName is stable for a Subscription and unique across workspaces.
func deliveryName(cluster, name string) string {
	sum := sha256.Sum256([]byte(cluster + "/" + name))
	return "sub-" + hex.EncodeToString(sum[:])[:16]
}
