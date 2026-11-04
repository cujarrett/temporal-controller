<p align="center">
  <img src=".github/logo.png" alt="The Go gopher hugging Ziggy, Temporal's tardigrade" width="700">
</p>

<h1 align="center">temporal-controller</h1>

<p align="center">
  Temporal namespaces as Kubernetes resources.<br>
  Self-hosted or in Temporal Cloud.<br>
  Cloud workers sign in with their SPIFFE identity.
</p>

<p align="center">
  <a href="https://github.com/cujarrett/temporal-controller/actions/workflows/ci.yml"><img src="https://github.com/cujarrett/temporal-controller/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
</p>

```yaml
apiVersion: temporal.local.lab/v1alpha1
kind: TemporalNamespace
metadata:
  name: foo
spec:
  retention: 72h                 # required, history kept for closed workflows
  description: Workflows for foo # optional, shown in the Temporal UI
```

The controller makes Temporal match the object. The Temporal namespace takes the object's name, and `Ready` turns `True` once it matches the spec.

## Why

Temporal's [Worker Controller](https://docs.temporal.io/production-deployment/worker-deployments/kubernetes-controller) rolls out workers into a namespace that must already exist. Temporal has no Kubernetes resource that creates one. This controller is that resource.

A platform layer such as Crossplane can add access and connection details on top. A team then gets a ready-to-use place for its workflows from one resource. [My implementation](https://github.com/cujarrett/homelab/tree/main/platform/workflow-space).

## What it does

- **Create:** registers the namespace and records which object owns it.
- **Drift:** every 5 minutes, puts retention and description back to the spec.
- **Conflict:** leaves alone a namespace another object owns, or one made by hand. `Ready` turns `False` with reason `Conflict`.
- **Delete:** removes the namespace with the object, but only if that object owns it.

## Identity

Every sign-in starts from a SPIFFE identity, an SVID that SPIRE issues to each workload.

| Who | Self-hosted | Temporal Cloud |
|---|---|---|
| Workers | Trade their SVID for an Entra token, which the platform layer grants a role | Use their X.509 SVID as the client certificate |
| Controller | Trades its JWT-SVID for an Entra token | Uses the platform's API key file, because Temporal Cloud's management API takes only API keys today |

Self-hosted needs Entra because Temporal's built-in authorizer reads roles from a token claim. Temporal Cloud checks the certificate itself.

## Temporal Cloud

Set `backend: public-cloud` to create the namespace in Temporal Cloud instead of the self-hosted server at `--temporal-address`.

```yaml
spec:
  retention: 24h
  backend: public-cloud
```

- **Who gets in:** Temporal Cloud trusts the SPIRE trust bundle, and lets in any workload in the object's own Kubernetes namespace, `spiffe://<trust domain>/ns/<namespace>/sa/*`.
- **Where to connect:** `status.publicCloud.namespace` and `status.publicCloud.endpoint`.
- **Background work:** Temporal Cloud creates, changes and deletes in about a minute. `Ready` stays `False` with reason `Provisioning` until it finishes, and the finalizer waits for the delete.
- **Retention:** counts in whole days, from 1 to 90.
- **Backend:** fixed at creation. Temporal can't move a namespace between servers.
- **Region:** always `aws-us-east-1`.
- **Owner record:** kept in the Temporal Cloud namespace's description, so `spec.description` is refused.
- **Turned off:** without `--temporal-cloud-api-key-file`, `Ready` turns `False` with reason `PublicCloudOff`.

The API key needs the Developer account role. It can create namespaces and manage only the ones it created.

## Run it

```bash
make install   # apply the CRD to your current cluster
make run       # run the controller locally against it
```

| Flag | Default | Sets |
|---|---|---|
| `--temporal-address` | `localhost:7233` | The Temporal frontend, as `host:port` |
| `--entra-tenant-id` | none | The Entra tenant to get Temporal tokens from |
| `--entra-client-id` | none | The controller's Entra app client ID |
| `--temporal-token-scope` | none | The Entra scope for Temporal |
| `--spiffe-socket` | `/var/run/secrets/spiffe.io/api.sock` | The SPIRE agent's Workload API socket |
| `--temporal-cloud-api-key-file` | none | A file holding the Temporal Cloud API key. Empty turns `public-cloud` off |

With the Entra flags unset, it sends no token. That suits a local dev server.

## Develop

```bash
make generate manifests   # after editing api/
make test                 # unit and envtest suites, with a fake Temporal
TEMPORAL_CLOUD_API_KEY_FILE=key go test ./internal/temporal/  # also a real Temporal Cloud namespace, about 2 minutes
make lint
```

To also test against a real server, run `temporal server start-dev` and set `TEMPORAL_ADDRESS=localhost:7233`.

---

<sub>Go gopher by Renée French, CC BY 4.0. Ziggy is Temporal's mascot.</sub>
