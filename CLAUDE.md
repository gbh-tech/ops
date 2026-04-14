# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`ops` is a Go CLI (Cobra + Viper) that unifies DevOps/SRE workflows —
Kubernetes context setup, container registry auth, Werf deployments, secrets,
and git helpers — across multiple cloud providers (AWS today; Azure partially
scaffolded).

## Common commands

```bash
# Build
go build -o ops

# Run tests
go test ./...
go test ./pkg/...           # single package
go test -run TestName ./pkg/utils   # single test

# Lint / vet
golangci-lint run
go vet ./...

# Tidy modules
go mod tidy

# Release builds (local, via goreleaser)
goreleaser build --clean --snapshot
```

Tool versions are pinned in `.tool-versions` (use `asdf install`): Go 1.23.6,
awscli 2.24.5, werf 2.26.4, goreleaser 2.7.0, Node 22.13.0 (Node is only for
release-it / commitlint tooling).

## Architecture

### Entry point and command tree
- `main.go` → `cmd.Execute()`.
- `cmd/root.go` wires all subcommands onto the root Cobra command and defines
  the global persistent `-e/--env` flag. `Version` is injected at build time
  via ldflags (`-X ops/cmd.Version=...`, see `.goreleaser.yaml`).
- Each subcommand lives in its own package under `cmd/`:
  `env`, `kube`, `registry`, `deploy`, `git`, `secrets`. Subcommands are
  small — they parse flags and delegate to `pkg/`.

### Configuration (`pkg/config`)
- `config.LoadConfig()` reads `.ops/config.yaml` via Viper (env prefix `OPS`,
  AutomaticEnv on). The loaded `OpsConfig` struct fans out to per-area configs
  (`AWSConfig`, `CloudConfig`, `DeploymentConfig`, `K8sConfig`,
  `RegistryConfig`, `WerfConfig`, etc.) with per-area `Check*` validators.
- `LoadConfig` also seeds `AWS_PROFILE` / `AWS_REGION` defaults so downstream
  shell-outs (aws, werf, kubectl) inherit them.
- The repo's own `.ops/config.yaml` is checked in and doubles as the example
  config; Werf/Helm values live under `.helm/`.
- **Provider dispatch pattern**: `cloud.provider` (aws/azure/gcp),
  `deployment.provider` (werf/ansible), and `registry.type` (ecr/acr/gcr)
  are validated enums that determine which `pkg/<provider>` code path runs.
  New providers slot in by adding a package under `pkg/` and extending the
  corresponding `Check*Config` switch.

### Cloud / platform packages (`pkg/`)
- `pkg/aws` — ECR login, EKS kubeconfig fetch (shells out to `aws`).
- `pkg/azure` — scaffolding; Azure checks are currently commented out in
  `LoadConfig`.
- `pkg/k8s` — kubeconfig assembly and auth helpers; consumed by `cmd/kube`.
- `pkg/werf` — builds Werf CLI invocations. `values.go` assembles
  `--values` / `--secret-values` args from `werf.values`, `werf.secrets`,
  and `werf.values_files` in config, layered with per-env files discovered
  under those directories.
- `pkg/github` — go-github client (used by `git tag-cleaner`).
- `pkg/utils` — shared helpers: `bin.go` (locate/run external binaries),
  `cobra.go` (flag plumbing), `git.go`, `env.go`, `system.go`,
  `registry.go`. Most commands rely on `utils.RunCommand`-style shell-outs
  rather than native SDKs.

### Werf / Helm layout
- `werf.yaml` + `.helm/` drive actual deployments. `werf.services` in
  `.ops/config.yaml` must match image names in `werf.yaml`.
- `.helm/values/` and `.helm/extra-values/` contain per-environment files;
  `.helm/secrets/` holds Werf-encrypted secrets (`.werf_secret_key` is the
  local key file).

### CI/CD
- `.github/workflows/build.yaml`, `lint.yml`, `release.yml`. Releases use
  GoReleaser + release-it (see `.release-it.json`); commit messages are
  validated by commitlint (`commitlint.config.json`).
