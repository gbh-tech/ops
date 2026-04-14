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

# Run a standalone database migration task
ops ecs db-migrate --env production --app my-app

# Remove old task definition revisions, keeping the latest 5 (default)
ops ecs cleanup --env production --app my-app --keep 5

# Tail recent CloudWatch logs for a service (default: last 10 minutes)
ops ecs logs --env production --app my-app --since 30m
```

**Subcommand flags:**

| Subcommand   | Flag           | Default | Description                                                 |
|--------------|----------------|---------|-------------------------------------------------------------|
| `deploy`     | `-a, --app`    |         | App name (required in mono-repo mode)                       |
|              | `-e, --env`    |         | Target environment (required)                               |
|              | `-t, --tag`    | `latest`| Container image tag                                         |
|              | `--app-config` |         | Override path to app config file                            |
| `render`     | `-a, --app`    |         | App name (required in mono-repo mode)                       |
|              | `-e, --env`    |         | Target environment (required)                               |
|              | `-t, --tag`    | `latest`| Container image tag                                         |
| `status`     | `-a, --app`    |         | App name (required in mono-repo mode)                       |
|              | `-e, --env`    |         | Target environment (required)                               |
| `wait`       | `-a, --app`    |         | App name (required in mono-repo mode)                       |
|              | `-e, --env`    |         | Target environment (required)                               |
| `rollback`   | `-a, --app`    |         | App name (required in mono-repo mode)                       |
|              | `-e, --env`    |         | Target environment (required)                               |
| `db-migrate` | `-a, --app`    |         | App name (required in mono-repo mode)                       |
|              | `-e, --env`    |         | Target environment (required)                               |
|              | `--app-config` |         | Override path to app config file                            |
| `cleanup`    | `-a, --app`    |         | App name (required in mono-repo mode)                       |
|              | `-e, --env`    |         | Target environment (required)                               |
|              | `--keep`       | `5`     | Number of task definition revisions to keep                 |
| `logs`       | `-a, --app`    |         | App name (required in mono-repo mode)                       |
|              | `-e, --env`    |         | Target environment (required)                               |
|              | `--since`      | `10m`   | Show logs since this duration ago                           |

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
