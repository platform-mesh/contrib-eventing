/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package config holds the settings of each component. The settings every
// Platform Mesh component shares (logging, health probes, shutdown) come
// from golang-commons.
package config

import (
	"os"
	"time"

	"github.com/spf13/pflag"
)

type SubscriptionControllerConfig struct {
	KCPKubeconfig          string
	APIExportEndpointSlice string
	// BrokerNamespace holds the Knative Broker and the Triggers.
	BrokerNamespace string
	// AllowedSinkHosts are the hosts sinks may point at. Empty allows none.
	AllowedSinkHosts []string
}

func NewSubscriptionControllerConfig() SubscriptionControllerConfig {
	return SubscriptionControllerConfig{
		KCPKubeconfig:          "/kcp/kubeconfig",
		APIExportEndpointSlice: "eventing.platform-mesh.io",
		BrokerNamespace:        os.Getenv("POD_NAMESPACE"),
	}
}

func (c *SubscriptionControllerConfig) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.KCPKubeconfig, "kcp-kubeconfig", c.KCPKubeconfig, "kubeconfig for the provider workspace in kcp")
	fs.StringVar(&c.APIExportEndpointSlice, "apiexport-endpointslice", c.APIExportEndpointSlice, "APIExportEndpointSlice to watch Subscriptions through")
	fs.StringVar(&c.BrokerNamespace, "broker-namespace", c.BrokerNamespace, "namespace of the Knative Broker and Triggers (default from POD_NAMESPACE)")
	fs.StringSliceVar(&c.AllowedSinkHosts, "allowed-sink-hosts", c.AllowedSinkHosts, "hosts sinks and dead letter sinks may point at, exact or *.suffix; empty allows none")
}

// KeycloakConfig is how components reach Keycloak, with a client of the master
// realm that only has view-events and view-users.
type KeycloakConfig struct {
	URL string
	// ClientIDFile and ClientSecretFile are the keys of the mounted Secret, named
	// like the ones security-operator writes for its clients.
	ClientIDFile     string
	ClientSecretFile string
	// UserClaim is the claim Platform Mesh uses as user id: email or sub.
	UserClaim string
	// NameTTL is how long a user's names are remembered.
	NameTTL time.Duration
}

func NewKeycloakConfig() KeycloakConfig {
	return KeycloakConfig{
		URL:              "http://keycloak-service.platform-mesh-system.svc.cluster.local:8080/keycloak",
		ClientIDFile:     "/var/run/secrets/keycloak/client_id",
		ClientSecretFile: "/var/run/secrets/keycloak/client_secret",
		UserClaim:        "email",
		NameTTL:          5 * time.Minute,
	}
}

func (c *KeycloakConfig) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.URL, "keycloak-url", c.URL, "Keycloak URL including its relative path")
	fs.StringVar(&c.ClientIDFile, "keycloak-client-id-file", c.ClientIDFile, "file with the id of the client of the master realm that has view-events and view-users")
	fs.StringVar(&c.ClientSecretFile, "keycloak-client-secret-file", c.ClientSecretFile, "file with the client's secret")
	fs.StringVar(&c.UserClaim, "user-claim", c.UserClaim, "claim Platform Mesh uses as user id, email or sub")
	fs.DurationVar(&c.NameTTL, "name-ttl", c.NameTTL, "how long user names are remembered")
}

type KeycloakAdapterConfig struct {
	KeycloakConfig
	// SkipRealms are realms that do not belong to an org.
	SkipRealms []string
	IngressURL string
	TokenFile  string
	Interval   time.Duration
	// Namespace holds the adapter's state ConfigMap.
	Namespace string
}

func NewKeycloakAdapterConfig() KeycloakAdapterConfig {
	return KeycloakAdapterConfig{
		KeycloakConfig: NewKeycloakConfig(),
		SkipRealms:     []string{"master", "welcome"},
		IngressURL:     "http://ingress.contrib-eventing.svc.cluster.local",
		TokenFile:      "/var/run/secrets/contrib-eventing/token",
		Interval:       5 * time.Second,
		Namespace:      os.Getenv("POD_NAMESPACE"),
	}
}

func (c *KeycloakAdapterConfig) AddFlags(fs *pflag.FlagSet) {
	c.KeycloakConfig.AddFlags(fs)
	fs.StringSliceVar(&c.SkipRealms, "skip-realms", c.SkipRealms, "Keycloak realms that do not belong to an org")
	fs.StringVar(&c.IngressURL, "ingress-url", c.IngressURL, "where to send events")
	fs.StringVar(&c.TokenFile, "token-file", c.TokenFile, "ServiceAccount token the ingress accepts")
	fs.DurationVar(&c.Interval, "interval", c.Interval, "how often to read the events")
	fs.StringVar(&c.Namespace, "namespace", c.Namespace, "namespace of the state ConfigMap (default from POD_NAMESPACE)")
}

type OpenFGAAdapterConfig struct {
	// KeycloakConfig is optional: without the secret file snapshots have no names.
	KeycloakConfig
	OpenFGAURL string
	// SkipStores are OpenFGA stores that do not belong to an org.
	SkipStores []string
	IngressURL string
	TokenFile  string
	Interval   time.Duration
	// Namespace holds the adapter's state ConfigMap.
	Namespace string
	// CoreKubeconfig reaches the workspace of the core.platform-mesh.io export,
	// to read account paths. Empty means events have no accountPath.
	CoreKubeconfig    string
	CoreEndpointSlice string
}

func NewOpenFGAAdapterConfig() OpenFGAAdapterConfig {
	return OpenFGAAdapterConfig{
		KeycloakConfig: NewKeycloakConfig(),
		OpenFGAURL:     "http://openfga.platform-mesh-system.svc.cluster.local:8080",
		SkipStores:     []string{"orgs"},
		IngressURL:     "http://ingress.contrib-eventing.svc.cluster.local",
		TokenFile:      "/var/run/secrets/contrib-eventing/token",
		Interval:       5 * time.Second,
		Namespace:      os.Getenv("POD_NAMESPACE"),

		CoreKubeconfig:    "/kcp/kubeconfig",
		CoreEndpointSlice: "core.platform-mesh.io",
	}
}

func (c *OpenFGAAdapterConfig) AddFlags(fs *pflag.FlagSet) {
	c.KeycloakConfig.AddFlags(fs)
	fs.StringVar(&c.OpenFGAURL, "openfga-url", c.OpenFGAURL, "OpenFGA HTTP API")
	fs.StringSliceVar(&c.SkipStores, "skip-stores", c.SkipStores, "OpenFGA stores that do not belong to an org")
	fs.StringVar(&c.IngressURL, "ingress-url", c.IngressURL, "where to send events")
	fs.StringVar(&c.TokenFile, "token-file", c.TokenFile, "ServiceAccount token the ingress accepts")
	fs.DurationVar(&c.Interval, "interval", c.Interval, "how often to read the changelog")
	fs.StringVar(&c.Namespace, "namespace", c.Namespace, "namespace of the state ConfigMap (default from POD_NAMESPACE)")
	fs.StringVar(&c.CoreKubeconfig, "core-kubeconfig", c.CoreKubeconfig, "kubeconfig for the workspace of the core.platform-mesh.io export, to read account paths; empty leaves them out")
	fs.StringVar(&c.CoreEndpointSlice, "core-endpointslice", c.CoreEndpointSlice, "APIExportEndpointSlice to read AccountInfo through")
}

type IngressConfig struct {
	ListenAddress string
	Audience      string
	// Publishers may send events for any org.
	Publishers    []string
	BrokerIngress string
	// BrokerNamespace holds the Knative Broker.
	BrokerNamespace string
}

func NewIngressConfig() IngressConfig {
	return IngressConfig{
		ListenAddress:   ":8080",
		Audience:        "contrib-eventing",
		BrokerIngress:   "http://broker-ingress.knative-eventing.svc.cluster.local",
		BrokerNamespace: os.Getenv("POD_NAMESPACE"),
	}
}

func (c *IngressConfig) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.ListenAddress, "listen-address", c.ListenAddress, "address to accept events on")
	fs.StringVar(&c.Audience, "audience", c.Audience, "audience publishers' tokens must be issued for")
	fs.StringSliceVar(&c.Publishers, "publishers", c.Publishers, "users that may publish for any org")
	fs.StringVar(&c.BrokerIngress, "broker-ingress", c.BrokerIngress, "Knative broker ingress")
	fs.StringVar(&c.BrokerNamespace, "broker-namespace", c.BrokerNamespace, "namespace of the Knative Broker (default from POD_NAMESPACE)")
}
