/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package cmd

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/spf13/cobra"

	"github.com/platform-mesh/contrib-eventing/internal/config"
	"github.com/platform-mesh/contrib-eventing/internal/keycloak"
	"github.com/platform-mesh/contrib-eventing/internal/publish"
	kcadapter "github.com/platform-mesh/contrib-eventing/internal/sources/keycloak"
	pmcontext "go.platform-mesh.io/golang-commons/context"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var keycloakAdapterCfg = config.NewKeycloakAdapterConfig()

var keycloakAdapterCmd = &cobra.Command{
	Use:   "keycloak-adapter",
	Short: "publish logins from Keycloak's event store",
	Run:   runKeycloakAdapter,
}

func init() {
	keycloakAdapterCfg.AddFlags(keycloakAdapterCmd.Flags())
}

func runKeycloakAdapter(_ *cobra.Command, _ []string) { // coverage-ignore
	cfg := keycloakAdapterCfg
	if cfg.Namespace == "" {
		log.Fatal().Msg("POD_NAMESPACE or --namespace must be set")
	}
	if cfg.UserClaim != kcadapter.ClaimEmail && cfg.UserClaim != kcadapter.ClaimSub {
		log.Fatal().Str("userClaim", cfg.UserClaim).Msg("--user-claim must be email or sub")
	}
	secret, err := os.ReadFile(cfg.ClientSecretFile)
	if err != nil {
		log.Fatal().Err(err).Msg("unable to read the Keycloak client secret")
	}
	ctrl.SetLogger(log.ComponentLogger("controller-runtime").Logr())
	ctx, _, shutdown := pmcontext.StartContext(log, cfg, defaultCfg.ShutdownTimeout)
	defer shutdown()

	restCfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("unable to load in-cluster config")
	}
	kube, err := ctrlruntimeclient.New(restCfg, ctrlruntimeclient.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		log.Fatal().Err(err).Msg("unable to create client")
	}

	pub := &publish.Publisher{Client: &http.Client{Timeout: 10 * time.Second}, TokenFile: cfg.TokenFile}
	kc := keycloak.NewClient(ctx, cfg.URL, cfg.ClientID, strings.TrimSpace(string(secret)))
	a := &kcadapter.Adapter{
		KC:         kc,
		Users:      &keycloak.Users{Client: kc, TTL: cfg.NameTTL},
		SkipRealms: cfg.SkipRealms,
		UserClaim:  cfg.UserClaim,
		Kube:       kube,
		Namespace:  cfg.Namespace,
		Interval:   cfg.Interval,
		Log:        log,
		Publish: func(ctx context.Context, e cloudevents.Event) error {
			return pub.Publish(ctx, cfg.IngressURL, e)
		},
	}
	log.Info().Str("keycloak", cfg.URL).Str("userClaim", cfg.UserClaim).Msg("starting adapter")
	if err := a.Run(ctx); err != nil {
		log.Fatal().Err(err).Msg("adapter stopped")
	}
}
