# temporal-controller

Not affiliated with Temporal Technologies.

A Kubernetes controller that manages Temporal resources as custom resources. You declare a resource in YAML, and the controller makes Temporal match it.

## Resources

| Kind | What it manages |
|---|---|
| `TemporalNamespace` | A namespace on a self-hosted Temporal server |

```yaml
apiVersion: temporal.local.lab/v1alpha1
kind: TemporalNamespace
metadata:
  name: foo
spec:
  retention: 72h
  description: Workflows for the foo team
```

- `metadata.name`: the Temporal namespace name.
- `retention`: how long Temporal keeps the history of closed workflows. Required.
- `description`: shown in the Temporal UI.

The `Ready` condition turns `True` once the namespace exists in Temporal with this spec.

## Behaviour

- **Create:** registers the namespace and records the object that owns it.
- **Drift:** every 5 minutes, sets retention and description back to the spec.
- **Conflict:** a namespace owned by another object, or made outside the controller, is left alone. `Ready` turns `False` with reason `Conflict`.
- **Delete:** deleting the object deletes the namespace, but only if this object owns it.

## Flags

| Flag | Default | What it sets |
|---|---|---|
| `--temporal-address` | `localhost:7233` | The Temporal frontend, as `host:port` |

## Development

| Command | What it does |
|---|---|
| `make generate manifests` | Regenerates deepcopy, CRD and RBAC after editing `api/` |
| `make test` | Runs unit tests and the envtest suite |
| `make lint` | Runs golangci-lint |
| `make build` | Builds the manager binary into `bin/` |
| `make install` | Applies the CRD to the current kubeconfig |
| `make run` | Runs the controller locally against the current kubeconfig |

`make test` uses a fake Temporal. To also run against a real server, start one with `temporal server start-dev` and set `TEMPORAL_ADDRESS=localhost:7233`.
