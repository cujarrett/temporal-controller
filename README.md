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

## Development

| Command | What it does |
|---|---|
| `make generate manifests` | Regenerates deepcopy, CRD and RBAC after editing `api/` |
| `make test` | Runs unit tests and the envtest suite |
| `make lint` | Runs golangci-lint |
| `make build` | Builds the manager binary into `bin/` |
| `make install` | Applies the CRD to the current kubeconfig |
| `make run` | Runs the controller locally against the current kubeconfig |
