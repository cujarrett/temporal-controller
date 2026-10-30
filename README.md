I'm<p align="center">
  <img src=".github/logo.png" alt="The Go gopher hugging Ziggy, Temporal's tardigrade" width="200">
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

Temporal runs workflows that survive crashes, restarts and long waits. Offering it to teams without tickets takes several pieces:

- **A namespace per team**, created, kept right and cleaned up.
- **Access** to that namespace and no other.
- **Connection details** handed to the team's apps.
- **Worker rollouts** that don't break running workflows.

This controller is the first piece, as a Kubernetes resource. A platform layer such as Crossplane puts it together with the others, so a team asks for "workflows" in one YAML file and gets all of them. [My implementation](https://github.com/cujarrett/homelab/tree/main/platform/workflow).

What it adds to that namespace:

- **Owned:** the namespace records which object created it, so one team cannot take or delete another team's.
- **Kept right:** changes made by hand in Temporal are set back to the YAML.
- **Cleaned up:** deleting the YAML deletes the namespace.

## What it does

- **Create:** registers the namespace and records which object owns it.
- **Drift:** every 5 minutes, puts retention and description back to the spec.
- **Conflict:** leaves a namespace another object owns, or one made outside the controller, alone. `Ready` turns `False` with reason `Conflict`.
- **Delete:** removes the namespace with the object, but only if that object owns it.

## Run it

| Flag | Default | Sets |
|---|---|---|
| `--temporal-address` | `localhost:7233` | The Temporal frontend, as `host:port` |

```bash
make install   # apply the CRD to your current cluster
make run       # run the controller locally against it
```

## Develop

```bash
make generate manifests   # after editing api/
make test                 # unit and envtest suites, with a fake Temporal
make lint
```

To also test against a real server, run `temporal server start-dev` and set `TEMPORAL_ADDRESS=localhost:7233`.

---

<sub>Go gopher by Renée French, CC BY 4.0. Ziggy is Temporal's mascot.</sub>
