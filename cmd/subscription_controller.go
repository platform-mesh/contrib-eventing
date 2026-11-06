/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/platform-mesh/contrib-eventing/api/v1alpha1"
	"github.com/platform-mesh/contrib-eventing/internal/config"
	"github.com/platform-mesh/contrib-eventing/internal/controller/subscription"
	"github.com/platform-mesh/contrib-eventing/internal/delivery/knative"
	pmcontext "go.platform-mesh.io/golang-commons/context"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/kcp-dev/multicluster-provider/apiexport"
	kcpapisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
)

var subscriptionControllerCfg = config.NewSubscriptionControllerConfig()

var subscriptionControllerCmd = &cobra.Command{
	Use:   "subscription-controller",
	Short: "turn Subscriptions in kcp into Knative Brokers and Triggers",
	Run:   runSubscriptionController,
}

func init() {
	subscriptionControllerCfg.AddFlags(subscriptionControllerCmd.Flags())
}

func runSubscriptionController(_ *cobra.Command, _ []string) { // coverage-ignore
	ctrl.SetLogger(log.ComponentLogger("controller-runtime").Logr())
	ctx, _, shutdown := pmcontext.StartContext(log, subscriptionControllerCfg, defaultCfg.ShutdownTimeout)
	defer shutdown()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	// The apiexport provider reads the APIExportEndpointSlice with this scheme.
	utilruntime.Must(kcpapisv1alpha1.AddToScheme(scheme))

	kcpCfg, err := clientcmd.BuildConfigFromFlags("", subscriptionControllerCfg.KCPKubeconfig)
	if err != nil {
		log.Fatal().Err(err).Msg("unable to load kcp kubeconfig")
	}
	// The kubeconfig from the platform-mesh-operator already points at the
	// provider workspace; make sure the host has no trailing slash.
	kcpCfg.Host = strings.TrimSuffix(kcpCfg.Host, "/")

	provider, err := apiexport.New(kcpCfg, subscriptionControllerCfg.APIExportEndpointSlice, apiexport.Options{Scheme: scheme})
	if err != nil {
		log.Fatal().Err(err).Msg("unable to create apiexport provider")
	}

	// Knative and the leader election lease live on the local cluster, not in kcp.
	localCfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("unable to load in-cluster config")
	}
	mgr, err := mcmanager.New(kcpCfg, provider, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: defaultCfg.Metrics.BindAddress},
		HealthProbeBindAddress: defaultCfg.HealthProbeBindAddress,
		LeaderElection:         defaultCfg.LeaderElectionEnabled,
		LeaderElectionID:       "subscription-controller.eventing.platform-mesh.io",
		LeaderElectionConfig:   localCfg,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("unable to create manager")
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Fatal().Err(err).Msg("unable to set up health check")
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Fatal().Err(err).Msg("unable to set up ready check")
	}

	localClient, err := ctrlruntimeclient.New(localCfg, ctrlruntimeclient.Options{Scheme: scheme})
	if err != nil {
		log.Fatal().Err(err).Msg("unable to create client for the local cluster")
	}
	r := &subscription.Reconciler{
		Manager:          mgr,
		Delivery:         &knative.Backend{Client: localClient, Namespace: subscriptionControllerCfg.BrokerNamespace},
		AllowedSinkHosts: subscriptionControllerCfg.AllowedSinkHosts,
	}
	if len(r.AllowedSinkHosts) == 0 {
		log.Warn().Msg("no --allowed-sink-hosts: no Subscription will get events")
	}
	if err := r.SetupWithManager(mgr); err != nil {
		log.Fatal().Err(err).Msg("unable to create controller")
	}

	log.Info().Msg("starting manager")
	if err := mgr.Start(ctx); err != nil {
		log.Fatal().Err(err).Msg("problem running manager")
	}
}
