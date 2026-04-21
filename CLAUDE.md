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
changesets / commitlint tooling).

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
  (`AWSConfig`, `AzureConfig`, `K8sConfig`, `ECSConfig`, `WerfConfig`,
  `RegistryConfig`, etc.).
- `LoadConfig` also seeds `AWS_PROFILE` / `AWS_REGION` defaults so downstream
  shell-outs (aws, werf, kubectl) inherit them, and runs `Check*` validators
  only for the active cloud provider.
- The repo's own `.ops/config.yaml` is checked in and doubles as the example
  config; Werf/Helm values live under `.helm/`.
- **Provider dispatch pattern**: cloud (`aws`/`azure`/`gcp`) and deployment
  (`ecs`/`werf`/`ansible`) providers are inferred from which provider blocks
  are defined in the config (`aws:`, `azure:`, `ecs:`, `werf:`, …). When
  multiple blocks coexist, the active provider is chosen by the top-level
  `provider:` / `deployment:` keys in the config or by the persistent
  `--provider` / `--deployment` CLI flags. The registry kind and URL are
  derived from the active cloud provider; set `registry.url` only when
  overriding the default. Use `cfg.CloudProvider()`,
  `cfg.DeploymentProvider()`, `cfg.RegistryType()`, `cfg.RegistryURL()` from
  call sites instead of reading the raw fields directly. The `ops config`
  command prints the resolved settings for the current invocation. New
  providers slot in by adding a package under `pkg/` and a block under
  `OpsConfig` plus an entry in `definedCloudBlocks` /
  `definedDeploymentBlocks` in `pkg/config/inference.go`.

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
- `.github/workflows/build.yaml`, `lint.yml`, `release.yaml`. Releases use
  Changesets + GoReleaser (see `.changeset/config.json`); commit messages are
  validated by commitlint (`commitlint.config.json`).
