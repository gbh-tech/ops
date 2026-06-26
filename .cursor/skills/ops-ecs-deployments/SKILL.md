---
name: ops-ecs-deployments
description: >-
  Operates GBH ops CLI ECS deployments: render, deploy, wait, status, logs,
  vars, secrets, db-migrate, schedule-run, run/shell, port-forward, rollback,
  and cleanup. Use when deploying or operating ECS services, running migrations,
  ad-hoc scheduled tasks, inspecting deploy config (deploy/config.toml,
  .ops/config.yaml), troubleshooting rollouts, or validating scheduled_tasks
  and database_migrations settings.
---

# Ops ECS Deployments

## Selection rules

- Use this skill when the repo deploys through the GBH `ops` CLI with
  `deployment: ecs` and `provider: aws`.
- Use `ops-ecs-service-config` when editing deploy TOML fields, adding env vars,
  secrets, volumes, or scheduled task definitions.
- Use `database-migration-ops` for migration safety, backup checks, and recovery.
- Use `run-command-shell-access` for interactive shells and one-off commands.
- Use `access-database-with-proxy` for database tunnels via `ops ecs db-proxy`.
- Use `deployment-troubleshooting` or `rollback-recovery` when deploys fail or
  need rollback.
- Use `kubernetes-manifest`, `helm-chart`, or werf skills for EKS/Kubernetes repos.

## Prerequisites

1. Confirm ECS workflow by reading `.ops/config.yaml`, deploy TOML, Taskfile
   wrappers, and CI that call `ops ecs`.
2. Required tooling:
   - `ops` binary built or installed from the ops-cli repo.
   - AWS credentials for deploy/status/wait/logs/migrate/schedule-run/rollback/
     cleanup (profile from `.ops/config.yaml` or env vars).
   - `aws` CLI and `session-manager-plugin` for `ops ecs run`, `shell`, and
     `port-forward` / `db-proxy`.
3. Config files:
   - Global: `.ops/config.yaml` (`ecs:` block -- cluster, roles, defaults,
     optional `scheduler`, `cleanup_keep`).
   - Per-app: `deploy/config.toml` (single-repo) or
     `apps/{app}/deploy/config.toml` (mono-repo, `repo_mode: mono`).
4. Flags:
   - `--env` is required on every `ops ecs` subcommand.
   - `--app` is required in mono-repo mode; optional in single-repo mode.
   - `--app-config` overrides the deploy TOML path (workers, alternate configs).
   - `--tag` defaults to the env name (e.g. `--env stage` pulls `:stage` image).

Check installed syntax before relying on flags:

```sh
ops ecs --help
ops ecs deploy --help
ops ecs render --help
```

## Config resolution

Three-layer merge for each deploy:

```
ecs.defaults (.ops/config.yaml) -> [global] -> [env section]
```

Key deploy TOML sections:

| Section | Purpose |
| ------- | ------- |
| `[global]` | App name, image, port, replicas, migrations, defaults |
| `[stage]`, `[production]`, etc. | Per-env overrides |
| `[global.environment]` / `[env.environment]` | Static env vars |
| `[global.secrets]` / `[env.secrets]` | Secrets Manager references |
| `[[global.scheduled_tasks]]` / `[[env.scheduled_tasks]]` | EventBridge schedules |

Important merge rules:

- Per-env `environment`, `secrets`, `build_secrets`, and `build_args` merge with
  global; env values win on conflict.
- Per-env `volumes` and `scheduled_tasks` REPLACE the global list entirely.
  Re-declare every task or volume you need in that env section.
- Only EFS volumes are safe in `[global]`; host/docker volumes belong in
  per-env sections.

TOML pitfall: after a `[section.subtable]` header, bare keys belong to that
subtable until the next header. Put scalar fields above subtable headers, or
use dedicated map headers like `[global.secrets]`.

Service naming: ECS operations target `name` from deploy TOML by default. Set
`append_environment = true` to use legacy `{name}-{env}` service names during
migration. Always verify with `ops ecs render` before deploying.

## Command reference

All examples use mono-repo flags; omit `--app` in single-repo mode.

### render -- dry-run (no AWS credentials)

Prints resolved task definition summary locally.

```sh
ops ecs render --app my-app --env stage
ops ecs render --app my-app --env stage --tag v1.2.3
ops ecs render --app my-app --env stage --app-config apps/my-app/deploy/worker.toml
```

Use before every deploy or config change to confirm service name, image, replicas,
migrations, volumes, and scheduled tasks.

### deploy -- register task def, migrate, update service

```sh
ops ecs deploy --app my-app --env stage
ops ecs deploy --app my-app --env stage --tag v1.2.3
ops ecs deploy --app my-app --env stage --skip-migrations
```

Deploy sequence:

1. Register service task definition (`{app}-{env}` family).
2. Register scheduled task definition when `scheduled_tasks` exist
   (`{app}-{env}-scheduled` family).
3. Run database migrations (unless skipped) when `database_migrations = true`,
   `migration_command` is set, and `replicas > 0`.
4. Update ECS service to new task definition and desired count.
5. Clean up old task definition revisions (default keep: 5).
6. Reconcile EventBridge Scheduler entries for configured scheduled tasks.

After deploy, run `ops ecs wait` to block until stable.

### wait -- block until service is stable

```sh
ops ecs wait --app my-app --env stage
```

Skipped automatically when `replicas = 0` (workers with no running service).

### status -- current service state

```sh
ops ecs status --app my-app --env stage
```

Shows service name, status, running/desired count, task definition ARN, last event.

### logs -- recent CloudWatch logs

```sh
ops ecs logs --app my-app --env stage
ops ecs logs --app my-app --env stage --since 30m
```

Default window is 10 minutes. Use after failed deploys, migrations, or
schedule-run tasks.

### vars -- resolved environment variables

```sh
ops ecs vars --app my-app --env stage
ops ecs vars --app my-app --env stage --format dotenv
ops ecs vars --app my-app --env stage --format dotenv --output -
```

Local-only (no AWS). Dotenv defaults to `apps/{app}/.env` or `.env`.

### secrets -- resolved Secrets Manager ARN references

```sh
ops ecs secrets --app my-app --env stage
```

Local-only. Shows env var to ARN mapping; never prints secret values.

### db-migrate -- standalone migration task

```sh
ops ecs db-migrate --app my-app --env stage
```

Runs `migration_command` as a one-off ECS task using the service's network
config. Requires `database_migrations = true` and `migration_command` set.
Skipped when `replicas = 0`. Prefer this over `deploy` when migrations must run
without a full rollout. Ask for explicit confirmation before production.

### schedule-run -- ad-hoc scheduled task

```sh
ops ecs schedule-run daily-cleanup --app my-app --env stage
```

Runs a named task from `[[global.scheduled_tasks]]` or `[[env.scheduled_tasks]]`
immediately using the `{app}-{env}-scheduled` task definition. Waits for
completion, prints CloudWatch logs, exits non-zero on failure.

### run / shell -- one-off command via ECS Exec

```sh
ops ecs run --app my-app --env stage --command "ls /app"
ops ecs shell --app my-app --env stage
ops ecs shell --app my-app --env stage --shell /bin/bash
```

Connects to the first running task of the service. Requires ECS Exec enabled,
`aws` CLI, and `session-manager-plugin`. Not for migrations -- use `db-migrate`
or framework-specific commands through `run` with explicit `--command`.

### port-forward / db-proxy -- local port tunnel via SSM

```sh
ops ecs port-forward --app my-app --env stage --port 8080
ops ecs port-forward --app my-app --env stage --port 8080 --local-port 18080
ops ecs db-proxy --env stage
```

`db-proxy` lists ECS services containing "db-proxy" and infers postgres/mysql/redis
ports. See `access-database-with-proxy` for database access workflows.

### rollback -- previous task definition revision

```sh
ops ecs rollback --app my-app --env stage
```

Rolls the service back one task definition revision. Does not revert database
schema changes. See `rollback-recovery` for production safety.

### cleanup -- prune old task definition revisions

```sh
ops ecs cleanup --app my-app --env stage
ops ecs cleanup --app my-app --env stage --keep 10
```

Cleans both `{app}-{env}` and `{app}-{env}-scheduled` families. Default keep
count comes from `ecs.cleanup_keep` in `.ops/config.yaml` (5 when unset). Deploy
also runs cleanup automatically.

## Database migrations

Configure in deploy TOML:

```toml
database_migrations = true
migration_command = ["bundle", "exec", "rails", "db:migrate"]
```

Behavior:

- On `ops ecs deploy`, migrations run automatically before the service update
  when `database_migrations = true`, `migration_command` is non-empty, and
  `replicas > 0`.
- Use `--skip-migrations` on deploy to bypass (logs a skip message).
- Use `ops ecs db-migrate` to run migrations without deploying.
- Migration failures abort deploy and print CloudWatch logs.
- Workers with `replicas = 0` skip migrations on both deploy and db-migrate.

Follow `database-migration-ops` for preflight, backup, production approval, and
recovery. Never expose secret values from connection strings.

## Scheduled tasks

Declare recurring one-off ECS tasks in deploy TOML:

```toml
[[global.scheduled_tasks]]
name     = "daily-cleanup"
schedule = "cron(0 3 * * ? *)"
command  = ["node", "scripts/cleanup.js"]
enabled  = true
```

Per-env tasks use `[[stage.scheduled_tasks]]`, `[[production.scheduled_tasks]]`,
etc. Env arrays replace global arrays -- do not assume inheritance.

Required fields per task: `name`, `schedule` (cron or rate), `command`.
Optional: `timezone`, `enabled`, `cpu`, `memory`, `capacity_provider`,
`flexible_window_minutes`, `description`.

Infrastructure prerequisites in `.ops/config.yaml`:

```yaml
ecs:
  scheduler:
    role_arn: "arn:aws:iam::ACCOUNT:role/ecs-scheduler-{env}"
    group_name: "{cluster}-{env}"
```

Both `role_arn` and `group_name` are required when any app declares
`scheduled_tasks`. Provision the IAM role and schedule group via Terraform
(aws-ecs module) before first deploy with schedules.

On deploy, ops reconciles EventBridge Scheduler: creates, updates, or deletes
schedules to match deploy TOML. Use `ops ecs schedule-run <name>` to test a
task without waiting for the cron.

Place `[[global.scheduled_tasks]]` entries after all scalar fields and subtable
headers in `[global]` to avoid the TOML subtable scoping trap.

## Standard deploy workflow

```sh
# 1. Validate resolved config (no AWS needed)
ops ecs render --app my-app --env stage

# 2. Deploy (runs migrations unless skipped)
ops ecs deploy --app my-app --env stage --tag stage

# 3. Wait for stability
ops ecs wait --app my-app --env stage

# 4. Verify
ops ecs status --app my-app --env stage
ops ecs logs --app my-app --env stage --since 10m
```

Production: get explicit user confirmation before deploy, migration, rollback,
or schedule-run.

Typical CI sequence: build and push image with env tag, then
`ops ecs deploy --env $ENV --app $APP --tag $TAG`, then `ops ecs wait`.

## Verification checklist

After deploy or config changes, confirm:

- [ ] `ops ecs render` shows expected service name, image, replicas, ports.
- [ ] Migration command appears when `database_migrations = true`.
- [ ] Scheduled task count and names match deploy TOML.
- [ ] `ops ecs status` shows running count equals desired count.
- [ ] `ops ecs logs` has no startup errors in the last 10-30 minutes.
- [ ] Health check path responds if the service exposes HTTP.

For scheduled task changes, run `ops ecs schedule-run <name>` in non-production
first when possible.

## Pitfalls and limitations

| Pitfall | Mitigation |
| ------- | ---------- |
| Wrong ECS service targeted | Run `ops ecs render`; check `append_environment` |
| TOML keys land in wrong section | Keep scalars above subtable headers |
| Env scheduled_tasks missing tasks | Env list replaces global; re-declare all tasks |
| Env volumes missing EFS mounts | Env volumes replace global entirely |
| Deploy pulls wrong image | `--tag` defaults to env name; match CI push tag |
| Migration fails mid-deploy | Check logs; fix command; use `db-migrate` to retry |
| Scheduled tasks fail on deploy | Verify `ecs.scheduler` in `.ops/config.yaml` |
| ECS Exec fails | Confirm running tasks, SSM agent, task role permissions |
| `render` vs `deploy` creds | `render` is local-only; deploy needs AWS |
| Production surprise | Always confirm env and get approval for prod ops |

**Not supported:** scheduled service replica scaling (time-based desired count
changes). Do not configure, document, or suggest scale schedules as an ops CLI
feature. Replica count is set via `replicas` in deploy TOML and applied on
deploy; there is no cron-based scale-up/scale-down in ops today.

## Output format

When completing ECS deployment work, report:

- Target app, environment, and config file path.
- Commands run (render, deploy, wait, logs, etc.).
- Validation results (status, log excerpts, migration outcome).
- Manual follow-ups (secrets in AWS, Terraform for scheduler/volumes, prod approval).
- Related skill handoffs when the task crosses into config editing, migrations,
  shell access, or incident recovery.
