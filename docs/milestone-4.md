# Milestone 4

Goal: someone outside the team installs eventing on their own Platform Mesh
from a release and tries it, and providers can publish their own events.

Two parts with their own issues: the installable module
([#13](https://github.com/platform-mesh/contrib-eventing/issues/13)) and
provider publishing
([#14](https://github.com/platform-mesh/contrib-eventing/issues/14)). They
don't depend on each other and can run in parallel. This answers decision 6
of [milestone 3](milestone-3.md).

It builds on milestone 3 (least privilege for the Keycloak adapter).

## Where we are

`hack/install.sh` adds eventing to a local kind Platform Mesh in eight steps.
It builds the image on the machine and loads it into kind
(`imagePullPolicy: Never`), creates the kcp provider workspace with the kcp
admin kubeconfig and patches `extraProviderConnections` into the
`PlatformMesh` resource. Nothing runs in CI, so pull requests merge without a
check.

The roadmap also lists automatic binding for new orgs. Nothing needs binding
today: Subscriptions live in the eventing workspace, and the adapters read
all orgs. Binding comes back with org-level subscriptions (later).

## Part 1: installable module

### How Platform Mesh installs a provider

A `ManagedProvider` (providers.platform-mesh.io, on the runtime cluster) is
the platform's way to onboard a service of its own. platform-mesh-operator
creates a `Provider` in `root:providers:system`, which gets a workspace
`root:providers:<name>-<cluster id>` and a kubeconfig with admin rights in
that workspace only. The operator copies the kubeconfig as a Secret to the
runtime cluster and deploys `spec.runtimeDeployments` through Flux, either a
chart from an OCI registry (`flux`) or a chart from an OCM component
(`ocm`), resolved by the ocm-controller.

The `platform-mesh-prerequisites` chart installs Flux and the ocm-controller
by default. The Tilt setup for development switches both off.

`ManagedProvider` does not cover the read connection to
`core.platform-mesh.io` in `root:platform-mesh-system`, which the adapter
needs for account paths (milestone 2). That stays an
`extraProviderConnections` entry, see decision 2.

### Work

1. **CI.** Build, unit tests, lint and the kind e2e on every pull request.
2. **Images.** Multi-arch images to ghcr on every tag.
3. **Chart.** Controller, ingress, adapters, CRD and NetworkPolicies from
   `config/`, configured by values instead of `${VAR}` substitution. The kind
   only parts (`hostAliases` to Traefik, local image) stay in the demo.
4. **kcp setup in the chart.** The schema, the APIExport and its binding are
   applied with the provider kubeconfig when eventing starts, instead of
   `20-kcp-provider.sh` with the kcp admin. The workspace name is no longer
   fixed, so docs and e2e stop hard coding `root:providers:eventing`.
5. **OCM component.** Chart and images as one OCM component, as Platform
   Mesh does it
   ([ADR 001](https://github.com/platform-mesh/architecture/blob/main/adr/001-sbom-generation-and-ocm-component-restructuring.md),
   [ADR 010](https://github.com/platform-mesh/architecture/blob/main/adr/010-consolidate-helm-charts-and-ocm.md)),
   published on every tag, installed through a `ManagedProvider` with `ocm`.
6. **Least privilege.** `readOnly: true` on the core connection
   (platform-mesh/platform-mesh#577) and the Keycloak reader from
   security-operator (platform-mesh/platform-mesh#578), once both are
   released. The Keycloak stopgap step goes away.
7. **Local install.** `hack/install.sh` installs the chart the same way and
   keeps only the demo extras: demo org, sample Subscription, event display.
8. **Docs.** An install guide in the README, including what delivery does
   and does not guarantee.

### Decisions

1. **Knative and NATS.** Eventing needs Knative Eventing for itself, because
   it locks down the broker ingress that all Brokers share, and NATS
   JetStream for the broker's channel. Today the install script applies both.
   - a) Prerequisites the operator installs, with pinned versions in the
     guide. The chart fails with a clear error if they are missing.
   - b) More entries in the same `ManagedProvider`. NATS has a chart,
     Knative Eventing ships only plain YAML, so it would go through the
     Knative operator.
   - c) Bundled into our chart.

   **Proposal: a)**: both are cluster wide and an operator may want to run
   them their own way. b) can come later without changing the chart.
2. **Core connection.**
   - a) The install guide adds one `extraProviderConnections` entry with
     `readOnly: true` to the `PlatformMesh` resource, next to the
     `ManagedProvider`. Both are plain resources, so GitOps works.
   - b) `ManagedProvider` gets a field for extra connections. A change in
     platform-mesh-operator, its owners have to agree.

   **Proposal: a)**, b) only if more providers need the same.
3. **Part of the Platform Mesh install.**
   - a) A separate `ManagedProvider` the operator adds.
   - b) A component in Platform Mesh's own OCM component, switched on in the
     `PlatformMesh` resource.

   **Proposal: a)**: it brings Knative and NATS and is still a contrib
   module. b) when it moves into core.
4. **e2e in CI.** It needs a Platform Mesh on kind.
   - a) Platform Mesh with Flux and the ocm-controller, eventing installed
     through the `ManagedProvider`, the way the guide says.
   - b) The chart installed with helm, the kcp parts by script.

   **Proposal: a)**: otherwise the documented path is never tested.

## Part 2: providers publish their own events

Today only the platform's own adapters may publish through the ingress. A
short ADR comes first: how a provider authenticates to the ingress, how it
declares the event types it publishes, and how the ingress limits it to orgs
that bound its APIExport. The ingress sets the org itself, never from the
event. Then the ingress change, a sample provider that publishes one event,
an e2e that also shows what a provider may not send, and a short guide for
providers. Details in the ADR.

## Order

1. CI and images, they need no decisions.
2. The ADR for part 2, in parallel.
3. Chart with the kcp setup, then the OCM component and the
   `ManagedProvider` install.
4. Least privilege once platform-mesh#577 and #578 are released.
5. `hack/install.sh` on the chart, e2e in CI on the `ManagedProvider` path.

Not in this milestone: more than one kcp shard (later), providers receiving
events, org and account level subscriptions.
