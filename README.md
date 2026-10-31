<p align="center">
  <img src=".github/logo.png" alt="The Go gopher hugging Ziggy, Temporal's tardigrade" width="700">
</p>

<h1 align="center">temporal-controller</h1>

<p align="center">
  Temporal namespaces as Kubernetes resources. Declare one in YAML, and the controller makes Temporal match it.
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

The Temporal namespace takes the object's name. `Ready` turns `True` once Temporal matches the spec.

## Why

Temporal's [Worker Controller](https://docs.temporal.io/production-deployment/worker-deployments/kubernetes-controller) rolls out workers into a namespace that must already exist. Temporal has no Kubernetes resource that creates one. This controller is that resource.

A platform layer such as Crossplane can pair it with access and connection details, so a team gets workflows from one YAML file. [My implementation](https://github.com/cujarrett/homelab/tree/main/platform/workflow).

## What it does

- **Create:** registers the namespace and records which object owns it.
- **Drift:** every 5 minutes, puts retention and description back to the spec.
- **Conflict:** leaves alone a namespace another object owns, or one made by hand. `Ready` turns `False` with reason `Conflict`.
- **Delete:** removes the namespace with the object, but only if that object owns it.

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

With the Entra flags unset, it sends no token. That suits a local dev server.

## Identity

The controller has no client secret. It signs in with its SPIFFE identity instead.

1. It fetches a short-lived JWT-SVID from the SPIRE agent.
2. Entra trusts that SVID through a federated credential and returns an access token.
3. Every Temporal call carries the token. Temporal reads its roles claim.

## Develop

```bash
make generate manifests   # after editing api/
make test                 # unit and envtest suites, with a fake Temporal
make lint
```

To also test against a real server, run `temporal server start-dev` and set `TEMPORAL_ADDRESS=localhost:7233`.

---

<sub>Go gopher by Renée French, CC BY 4.0. Ziggy is Temporal's mascot.</sub>
