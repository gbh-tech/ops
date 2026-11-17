<!-- omit in toc -->
# :hammer_and_wrench: gbh.tech - Ops CLI

<!-- omit in toc -->
## :books: Content

- [:memo: Overview](#memo-overview)
- [:zap: Requirements](#zap-requirements)
  - [System](#system)
  - [External](#external)
  - [Development](#development)
- [:package: Installation](#package-installation)
  - [Install script (recommended)](#install-script-recommended)
  - [From Source](#from-source)
  - [Using `go install`](#using-go-install)
  - [Pre-built binaries](#pre-built-binaries)
  - [Development Setup](#development-setup)
- [:rocket: How to use](#rocket-how-to-use)
  - [Basic Usage](#basic-usage)
  - [Configuration](#configuration)
- [:gear: Commands](#gear-commands)
  - [`env`](#env)
  - [`kube`](#kube)
  - [`registry`](#registry)
  - [`deploy`](#deploy)
  - [`ecs`](#ecs)
  - [`drift`](#drift)
  - [`git`](#git)
  - [`secrets`](#secrets)
  - [Global flags](#global-flags)
- [:test\_tube: Test](#test_tube-test)
  - [Running tests](#running-tests)
  - [Linting](#linting)
  - [Build](#build)
  - [CI/CD](#cicd)
- [:handshake: Contributing](#handshake-contributing)
- [:page\_facing\_up: License](#page_facing_up-license)
- [:speech\_balloon: Support](#speech_balloon-support)

## :memo: Overview

**Ops** is an all-purpose deployment automation tool tailored for DevOps &
SRE teams. Built in Go, it provides a unified CLI interface for managing
deployments, Kubernetes configurations, container registries, secrets, and
various DevOps operations across different cloud environments (AWS, Azure,
and more).

This tool streamlines common DevOps workflows by combining multiple operations
into a single, easy-to-use command-line interface, reducing the complexity of
managing multi-cloud deployments and infrastructure operations.

## :zap: Requirements

### System

- **Go**: 1.23.6 or later
- **Operating System**: macOS, Linux, or Windows

### External

The following tools are required for full functionality:

- **AWS CLI**: 2.24.5 or later (for AWS operations)
- **Werf**: 2.26.4 or later (for deployment operations)
- **kubectl**: For Kubernetes operations
- **Docker**: For container operations

### Development

- **Node.js**: 22.13.0 (for development and release automation)
- **GoReleaser**: 2.7.0 (for building releases)

## :package: Installation

### Install script (recommended)

Install the latest release into `~/.ops/bin` (no sudo, adds itself to your
shell `PATH`):

```bash
curl -fsSL https://ops.gbh.tech/install | bash
```

Pin a specific version:

```bash
curl -fsSL https://ops.gbh.tech/install | bash -s -- v1.3.0
# or
OPS_VERSION=v1.3.0 curl -fsSL https://ops.gbh.tech/install | bash
```

Override the install location or opt out of the PATH edit:

```bash
OPS_INSTALL_DIR=/opt/ops OPS_NO_MODIFY_PATH=1 \
  curl -fsSL https://ops.gbh.tech/install | bash
```

Supported platforms: Darwin (arm64, x86_64) and Linux (arm64, x86_64, i386) —
matching the archives produced by [`.goreleaser.yaml`](.goreleaser.yaml). The
script downloads the release tarball from
[GitHub Releases](https://github.com/gbh-tech/ops/releases), verifies its
SHA-256 checksum, extracts the binary, and updates `~/.bashrc`, `~/.zshrc`,
`~/.bash_profile`, or `~/.config/fish/config.fish` as appropriate.

The script is served by the [`worker/`](worker) Cloudflare Worker; the
script source lives at [`install.sh`](install.sh) and can also be used via
`curl -fsSL https://raw.githubusercontent.com/gbh-tech/ops/main/install.sh | bash`
as a fallback.

### From Source

1. Clone the repository:

   ```bash
   git clone <repository-url>
   cd ops
   ```

2. Build from source:

   ```bash
   go build -o ops
   ```

3. Install globally (optional):

   ```bash
   go install
   ```

### Using `go install`

```bash
go install github.com/gbh-tech/ops@latest
```

### Pre-built binaries

Download the latest binary from the
[releases page](https://github.com/gbh-tech/ops/releases).

### Development Setup

If you're contributing to the project:

1. Install development tools:

   ```bash
   # Using asdf (recommended)
   asdf install

   # Or install tools manually according to .tool-versions
   ```

2. Install dependencies:

   ```bash
   go mod download
   ```

## :rocket: How to use

The `ops` tool is designed with a simple command structure that follows the
pattern:

```bash
ops [command] [subcommand] [flags]
```

### Basic Usage

1. Check version:

   ```bash
   ops --version
   ```

2. Get help:

   ```bash
   ops --help
   ops [command] --help
   ```

3. Set target environment:
   Most commands support the `-e` or `--env` flag to specify the target
   environment:

   ```bash
   ops [command] --env production
   ```

### Configuration

The tool uses configuration files to manage different environments and
settings. Configure your environments and cloud credentials according to your
infrastructure setup.

## :gear: Commands

### `env`

```bash
# Display current environment
ops env

# Set specific environment
ops env --env staging
```

### `kube`

```bash
# Configure kubectl context for AWS EKS
ops kube config --env production

# Configure kubectl context for Azure AKS
ops kube config --env staging
```

### `registry`

```bash
# Login to container registry (ECR, etc.)
ops registry login --env production
```

### `deploy`

```bash
# Deploy using Werf
ops deploy werf --env production

# Deploy with custom values
ops deploy werf --env staging --set image.tag=v1.2.3
```

### `ecs`

Manages deployments, migrations, and operations for ECS-based services.
The `--app` flag is optional in single-repo mode and required in mono-repo mode
(`repo_mode: mono` in `.ops/config.yaml`).
Cluster names in `.ops/config.yaml` can include `{env}` placeholders (for example
`ecs.cluster: lighthouse-platform-{env}`), which are resolved per `--env` for ECS
API calls.

```bash
# Deploy an app: register task definition, run migrations, update service
ops ecs deploy --env production --app my-app --tag v1.2.3

# Dry-run: print the resolved task definition without deploying
ops ecs render --env staging --app my-app --tag v1.2.3

# Show current ECS service status
ops ecs status --env production --app my-app

# Wait for the service to reach a stable state
ops ecs wait --env production --app my-app

# Roll back the service to the previous task definition revision
ops ecs rollback --env production --app my-app

# Run a standalone migration, including at zero replicas; requires an existing ECS service with valid network configuration
ops ecs db-migrate --env production --app my-app

# Run a configured scheduled task immediately
ops ecs schedule-run daily-cleanup --env production --app my-app

# Remove old task definition revisions, keeping the latest 5 (default)
ops ecs cleanup --env production --app my-app --keep 5

# Tail recent CloudWatch logs for a service (default: last 10 minutes)
ops ecs logs --env production --app my-app --since 30m
```

Automatic migrations during `ops ecs deploy` run only when the app has more
than zero replicas. The explicit `ops ecs db-migrate` command can run for an app
with `replicas = 0`; both paths still require `database_migrations = true` and a
non-empty `migration_command`, and explicit migration requires an existing ECS
service resource that provides valid network configuration.

ECS service operations target `name` exactly from the app's `deploy/config.toml`
by default. This is a breaking change from the legacy `{name}-{env}` service
target. Set `append_environment = true` in `[global]` or an environment section
to opt in to the legacy behavior for services that still use suffixed names. For
example, `[stage] append_environment = true` lets `name = "subgraph-users"`
deploy the `subgraph-users-stage` task family to the existing
`subgraph-users-stage` ECS service, while the default deploys to
`subgraph-users`. Run `ops ecs render --env stage --app subgraph-users` to
verify the resolved service target and append setting before deploying.

### `drift`

Compares an environment with a manifest and lists the units that do not run the
image the manifest names. It only reads; deciding what to deploy, and deploying
it, stay with the caller. A deployment pipeline can use it to roll every unit
whose image differs from the manifest, not only what one commit changed, so a
unit that a failed deployment left on an older image is picked up by the next
one.

A unit is an app that `ops catalog` discovers: a directory under `app_dirs`
with a `deploy/config.toml`, whose `[global]` section sets its `name` (the unit
ID) and its `kind`.

The manifest is a JSON file whose `applications` object maps unit IDs to image
tags. The object is required and a tag cannot be empty. An `environment` field,
when present, must match `--env`. Other fields are ignored.

```json
{
  "environment": "stage",
  "applications": {
    "api": "1.4.0-0a1b2c3",
    "worker": "1.4.0-0a1b2c3"
  }
}
```

```bash
# Show every unit of an environment: ID, kind, status and reason
ops drift --env stage --manifest deploy/environments/stage.json

# The IDs that are not in sync, comma-separated (an empty line when none)
ops drift --env stage --manifest deploy/environments/stage.json --format ids

# Every unit as JSON
ops drift --env stage --manifest deploy/environments/stage.json --format json

# Exit with status 1 when a unit is not in sync, or when nothing was compared,
# for a gate
ops drift --env stage --manifest deploy/environments/stage.json --exit-code
```

Each unit of kind `ecs` or `lambda` that the manifest names gets one status:

| Status        | Meaning                                                                                                     |
| ------------- | ----------------------------------------------------------------------------------------------------------- |
| `in-sync`     | It runs the image of its manifest tag                                                                       |
| `out-of-sync` | It runs another image, or a rollout is not complete. A deployment at the manifest tag brings it in sync      |
| `missing`     | Its service, function or image repository does not exist in the environment. Provision it; a deployment fails until then |
| `unreadable`  | Its state could not be read                                                                                 |

`--format ids` lists every unit that is not `in-sync`, so a caller that deploys
the list never skips a unit it could not read. A `missing` unit is in the list
too, and its deployment fails until its infrastructure exists; read the
statuses from `--format json` to stop before deploying.

- An ECS unit is in sync when the task definition of its service names the
  image a deployment at that tag would, its rollout is complete, and every
  running task has that tag's digest. A service scaled to zero is compared by
  its task definition alone. An image outside the registry is compared by its
  reference, without a digest, and an image the unit's config pins to its own
  tag or digest is compared with that one.
- Only the service is compared. The scheduled tasks of a unit and the
  schedules that run them are not, so a deployment that updated the service and
  then failed to update the schedules still reads as in sync.
- A Lambda unit is in sync when its last update succeeded and its unpublished
  version (`$LATEST`) runs that tag's digest. ops does not deploy Lambda
  functions, so tell it how they are named with `lambda.function_name` in
  `.ops/config.yaml`, for example
  `lambda.function_name: my-platform-{service}-{env}`. Its image is looked up
  in the same registry repository a service of that name would use.
- The tag of a multi-platform image names an index. A unit that runs any image
  the index lists is in sync.
- The registry is read where `registry.url` points, which can be another
  account or region. The caller needs `ecr:DescribeImages` and
  `ecr:BatchGetImage` there.
- Units of any other kind have no running state and are left out, as are
  manifest entries that are not in the catalog and catalog units the manifest
  does not name.

### `git`

```bash
# Extract ticket ID from current branch
ops git get-ticket-id

# Clean up old Git tags
ops git tag-cleaner
```

### `secrets`

```bash
# Encrypt secrets
ops secrets encrypt --env production

# Decrypt secrets
ops secrets decrypt --env staging
```

### Global flags

- `-e, --env string`: Specify the target environment
- `-h, --help`: Show help for any command
- `--version`: Show version information

## :test_tube: Test

### Running tests

Currently, the project uses Go's built-in testing framework. To run tests:

```bash
# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./...

# Run tests for a specific package
go test ./pkg/...
```

### Linting

The project uses automated linting in CI/CD. To run linting locally:

```bash
# Using golangci-lint (if installed)
golangci-lint run

# Or use go vet
go vet ./...
```

### Build

Verify the build works correctly:

```bash
# Build the project
go build

# Run built binary
./ops --version
```

### CI/CD

The project uses GitHub Actions for automated testing and building:

- **Build**: Automated builds on pull requests
- **Lint**: Code quality checks
- **Release**: Automated releases using GoReleaser

## :handshake: Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests and linting
5. Submit a pull request

## :page_facing_up: License

See the [LICENSE](LICENSE) file for details.

## :speech_balloon: Support

For questions, issues, or contributions, please contact `devops@gbh.tech`.
