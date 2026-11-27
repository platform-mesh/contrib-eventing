# Milestone 4

Goal: someone outside the team installs eventing on their own Platform Mesh
from a release and tries it.

Tenant-scoped subscriptions are not part of the epic: milestone 2 decided on
subscriptions for platform admins, filtered by org.

The installable module has its own issue
([#13](https://github.com/platform-mesh/contrib-eventing/issues/13)).
Provider publishing
([#14](https://github.com/platform-mesh/contrib-eventing/issues/14)) was
planned as a second part here (decision 6 of [milestone 3](milestone-3.md))
and is now milestone 6 of the [roadmap](roadmap.md), since it is big enough
for its own milestone.

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

## How Platform Mesh installs a provider

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

## Work

1. **CI.** Build, unit tests, lint and the kind e2e on every pull request.
2. **Images.** Multi-arch images to ghcr on every tag.
3. **Chart.** Controller, ingress, adapters and NetworkPolicies from
   `config/`, configured by values instead of `${VAR}` substitution. The kind
   only parts (`hostAliases` to Traefik, local image) are values the demo sets.
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

## Decisions

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
5. **Namespace.** A `ManagedProvider` has to be in the namespace of its
   `PlatformMesh`, and the operator installs the chart there. So eventing runs
   in `platform-mesh-system`, next to the platform. Names get an `eventing-`
   prefix, the NetworkPolicies stay in `knative-eventing`.

## Order

1. CI and images, they need no decisions.
2. Chart with the kcp setup, then the OCM component and the
   `ManagedProvider` install.
3. Least privilege once platform-mesh#577 and #578 are released.
4. `hack/install.sh` on the chart, e2e in CI on the `ManagedProvider` path.
5. Before the first release, one manual run on a kcp with two shards. CI has
   one shard, and the adapter's `AccountInfo` watch should follow every shard.

Not in this milestone: provider publishing (milestone 6), more than one kcp
shard in CI, providers receiving events, org and account level
subscriptions.
