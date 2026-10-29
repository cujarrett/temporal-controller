# temporal-controller

## Rules

- **Never run `git add`, `git commit`, `git push`, or any git command that writes to the index, history or remotes.** Output the commands for the user to run. Staging is part of their review.
- **Never add a `Co-Authored-By` trailer or a "Generated with Claude Code" line** to commit messages or PR descriptions.
- **Always suggest a commit message** when work is ready to commit.
- **Give `git add` and the commit as two separate steps.** List every file by name. Never `git add .` or a bare directory.
- **Start `git add` with a `cd` to this repo's absolute path.**
- **Never output a `git push` command.**
- **Before telling the user to commit, run `/security-review`.** Report one line if clean. Spend words only on real findings.
- **Cheapest rung that works.** Skip the feature, reuse code, standard library, a dependency already installed, one line, then build the minimum.
- **Never hand-edit generated files.** See [Generated files](#generated-files).

## Build

`make` is the entrypoint. The project is generated around kubebuilder's Makefile, and its GitHub workflows call it.

Fast loop without envtest binaries: `go test -race -skip TestControllers ./...`.

Tests against a real Temporal skip unless `TEMPORAL_ADDRESS` is set. Run them after any change to `internal/temporal/` or the reconcile flow, with `temporal server start-dev` running. The fake cannot show Temporal's cache delays.

## Writing

Plain language in comments, docs and commit messages.

- One idea per sentence, most under 15 words.
- Comments are two or three lines. Say why the code is the way it is, never what the line does.
- State current rationale only. No "used to" or "was previously".
- Never an em dash or a spaced hyphen doing a dash's job.

## Philosophy

Grug-brained: say no to complexity, no abstraction until a pattern repeats three times, boring obvious code wins, understand why code exists before removing it.

# Kubebuilder

## Layout

| Path | What it holds |
|---|---|
| `api/v1alpha1/*_types.go` | CRD schemas and `+kubebuilder` markers |
| `internal/controller/` | Reconcile logic |
| `cmd/main.go` | Manager setup that registers each controller |
| `config/samples/` | Example resources |

## Generated files

Never edit these by hand.

- `config/crd/bases/*.yaml` and `config/rbac/role.yaml`: run `make manifests`
- `**/zz_generated.*.go`: run `make generate`
- `PROJECT`: written by the `kubebuilder` CLI

## CLI rules

- Add a kind with `kubebuilder create api --group temporal --version v1alpha1 --kind <Kind>`. Never create those files by hand.
- Keep every `// +kubebuilder:scaffold:*` marker. The CLI inserts code at them.
- Keep files where the CLI put them. It expects that layout.

## After a change

- Edited `*_types.go` or markers: `make manifests generate`
- Edited Go code: `make lint-fix test`

The e2e tests (`make test-e2e`) build an image and deploy it. Run them only against a throwaway kind cluster.
