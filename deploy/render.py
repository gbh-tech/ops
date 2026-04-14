#!/usr/bin/env python
"""
Render ECS task definition JSON from TOML configuration.

Reads a shared base config and an app-specific config, merges [global]
defaults with environment-specific overrides, expands secret ARN
references, and outputs a valid ECS task definition JSON document.

Usage:
    render.py --app NAME --env ENV [--image-tag TAG]            # JSON to stdout
    render.py --app NAME --env ENV --dry-run                    # summary to stderr + JSON to stdout
    render.py --app NAME --env ENV --image-tag TAG --output FILE # JSON to FILE, metadata to stdout
    render.py --app NAME --env ENV --metadata                   # metadata only to stdout

Examples:
    render.py --app interview-service --env production --image-tag v1.2.3
    render.py --app excalidraw --env production --dry-run
    render.py --app excalidraw --env production --output /tmp/taskdef.json
"""

from __future__ import annotations

import argparse
import json
import shlex
import sys
import tomllib
from pathlib import Path


def deep_merge(base: dict, override: dict) -> dict:
    """Recursively merge override into base. Override values win."""
    result = base.copy()
    for key, value in override.items():
        if key in result and isinstance(result[key], dict) and isinstance(value, dict):
            result[key] = deep_merge(result[key], value)
        else:
            result[key] = value
    return result


def load_toml(path: Path) -> dict:
    with open(path, "rb") as f:
        return tomllib.load(f)


def resolve_config_global(base: dict, app_config: dict) -> dict:
    """Merge base defaults -> app [global] only (no env-named sections)."""
    defaults = base.get("defaults", {})
    global_section = {k: v for k, v in app_config.get("global", {}).items() if k != "secrets"}
    return deep_merge(defaults, global_section)


def resolve_config(base: dict, app_config: dict, env: str) -> dict:
    """Merge base defaults -> app [global] -> app [env].

    Secrets are excluded here and handled separately by resolve_secrets()
    so we can distinguish shared vs env-specific paths.
    """
    merged = resolve_config_global(base, app_config)
    env_section = {k: v for k, v in app_config.get(env, {}).items() if k != "secrets"}
    return deep_merge(merged, env_section)


def resolve_secrets(
    app_config: dict, env: str, service_name: str, arn_prefix: str,
) -> list[dict]:
    """Build ECS secrets list from consolidated Secrets Manager secrets.

    Each service uses up to two secrets in Secrets Manager:
      - {service_name}/shared   (keys that apply to all envs)
      - {service_name}/{env}    (env-specific keys)

    Both are JSON blobs. Individual keys are referenced via the ECS
    valueFrom format: arn:...:secret:secret-name:json-key::

    Config supports two formats:
      List:  secrets = ["KEY_A", "KEY_B"]        (env var name = JSON key)
      Dict:  [section.secrets]
             ENV_VAR = "json_key"                (when names differ)

    If the same env var appears in both global and env, the env entry wins.
    """
    global_secrets = app_config.get("global", {}).get("secrets", {})
    env_secrets = app_config.get(env, {}).get("secrets", {})

    global_map = _normalize_secrets(global_secrets)
    env_map = _normalize_secrets(env_secrets)

    shared_arn = f"{arn_prefix}:{service_name}/shared"
    env_arn = f"{arn_prefix}:{service_name}/{env}"

    result = []
    for env_var, json_key in global_map.items():
        if env_var not in env_map:
            result.append({
                "name": env_var,
                "valueFrom": f"{shared_arn}:{json_key}::",
            })
    for env_var, json_key in env_map.items():
        result.append({
            "name": env_var,
            "valueFrom": f"{env_arn}:{json_key}::",
        })
    return result



def _normalize_secrets(secrets: dict | list) -> dict[str, str]:
    """Normalize secrets config to {env_var: json_key} dict.

    List form:  ["KEY_A", "KEY_B"] → {"KEY_A": "KEY_A", "KEY_B": "KEY_B"}
    Dict form:  {"ENV_VAR": "json_key"} → passed through as-is
    """
    if isinstance(secrets, list):
        return {key: key for key in secrets}
    return dict(secrets)


def format_environment(env_vars: dict) -> list[dict]:
    """Convert dict of env vars to ECS environment format."""
    return [
        {"name": name, "value": str(value)}
        for name, value in env_vars.items()
    ]


def compute_names(config: dict, env: str, cluster: str) -> dict:
    """Compute ECS resource names from merged config.

    Returns a dict with family, service, and log_group — derived once and
    used both by build_task_definition and the metadata output so there is
    no duplication.
    """
    app_name = config["name"]
    family = f"{app_name}-{env}"
    log_group = f"/ecs/{cluster}/{env}/{app_name}"
    return {"family": family, "service": family, "log_group": log_group}


def build_task_definition(
    base: dict, config: dict, names: dict, env: str, image_tag: str, secrets: list[dict],
) -> dict:
    """Build a complete ECS task definition from merged config."""
    aws = base["aws"]
    ecs = base["ecs"]

    app_name = config["name"]

    # Image URI: if image contains a '/', treat as an external/pre-built image.
    # Use it as-is if it already has a tag (contains ':' after the last '/'),
    # otherwise append the image tag. For ECR images, always append the tag.
    ecr_url = aws["ecr_url"]
    image_repo = config.get("image", app_name)
    if "/" in image_repo:
        repo_basename = image_repo.rsplit("/", 1)[-1]
        image = image_repo if ":" in repo_basename else f"{image_repo}:{image_tag}"
    else:
        image = f"{ecr_url}/{env}/{image_repo}:{image_tag}"

    port = config.get("port")

    # --- Container definition ---
    container: dict = {
        "name": app_name,
        "image": image,
        "essential": True,
    }

    if port:
        container["portMappings"] = [
            {"containerPort": port, "protocol": "tcp"}
        ]

    env_vars = config.get("environment", {})
    if env_vars:
        container["environment"] = format_environment(env_vars)

    command = config.get("command")
    if command:
        container["command"] = command

    if secrets:
        container["secrets"] = secrets

    health_path = config.get("health_check_path")
    if health_path and port:
        hc = config.get("container_health_check", config.get("health_check", {}))
        container["healthCheck"] = {
            "command": [
                "CMD-SHELL",
                f"curl -f http://localhost:{port}{health_path} || exit 1",
            ],
            "interval": hc.get("interval", 30),
            "timeout": hc.get("timeout", 5),
            "retries": hc.get("retries", 3),
            "startPeriod": hc.get("start_period", 60),
        }

    container["logConfiguration"] = {
        "logDriver": config.get("log_driver", "awslogs"),
        "options": {
            "awslogs-group": names["log_group"],
            "awslogs-region": aws["region"],
            "awslogs-stream-prefix": app_name,
        },
    }

    # --- Task definition ---
    task_def: dict = {
        "family": names["family"],
        "networkMode": config.get("network_mode", "awsvpc"),
        "requiresCompatibilities": [config.get("launch_type", "FARGATE")],
        "cpu": str(config.get("cpu", 256)),
        "memory": str(config.get("memory", 512)),
        "containerDefinitions": [container],
    }

    # IAM roles - prefer app-level override, fall back to base
    # Base roles may contain {service} and {env} placeholders
    execution_role = config.get("execution_role", ecs.get("execution_role", ""))
    task_role = config.get("task_role", ecs.get("task_role", ""))
    execution_role = execution_role.format(service=app_name, env=env)
    task_role = task_role.format(service=app_name, env=env)

    if execution_role:
        task_def["executionRoleArn"] = execution_role
    if task_role:
        task_def["taskRoleArn"] = task_role

    return task_def


def print_metadata(
    names: dict,
    app_name: str,
    taskdef_file: str | None = None,
    desired_count: int | None = None,
    migrations: bool = False,
    migration_command: list | None = None,
) -> None:
    """Print shell-sourceable ECS resource names to stdout."""
    print(f"ECS_FAMILY={names['family']}")
    print(f"ECS_SERVICE={names['service']}")
    print(f"ECS_APP_NAME={app_name}")
    print(f"LOG_GROUP={names['log_group']}")
    if taskdef_file:
        print(f"TASKDEF_FILE={taskdef_file}")
    if desired_count is not None:
        print(f"ECS_DESIRED_COUNT={desired_count}")
    if migrations:
        if not migration_command:
            print(
                "Error: database_migrations is true but migration_command is not set",
                file=sys.stderr,
            )
            sys.exit(1)
        print("ECS_MIGRATIONS=true")
        print(f"ECS_MIGRATION_COMMAND={shlex.quote(json.dumps(migration_command))}")


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Render ECS task definition from TOML config",
    )
    parser.add_argument("--app", required=True, help="App directory name under apps/")
    parser.add_argument(
        "--env",
        required=True,
        help="Target environment (stage, production).",
    )
    parser.add_argument("--image-tag", dest="image_tag", help="Image tag (default: latest)")
    parser.add_argument(
        "--output",
        metavar="FILE",
        help="Write task definition JSON to FILE; print shell metadata (ECS_FAMILY, "
        "ECS_SERVICE, TASKDEF_FILE, LOG_GROUP) to stdout.",
    )
    parser.add_argument(
        "--metadata",
        action="store_true",
        help="Print shell metadata only (ECS_FAMILY, ECS_SERVICE, LOG_GROUP) without "
        "rendering a full task definition.",
    )
    parser.add_argument("--base-config", dest="base_config", help="Path to base.toml override")
    parser.add_argument("--app-config", dest="app_config", help="Path to app config.toml override")
    parser.add_argument("--root", help="Repository root (auto-detected from script location)")
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print rendered config summary to stderr and JSON to stdout.",
    )
    args = parser.parse_args()

    # Resolve paths
    root = Path(args.root) if args.root else Path(__file__).resolve().parent.parent.parent

    base_path = Path(args.base_config) if args.base_config else root / ".ops" / "deploy" / "base.toml"
    app_path = Path(args.app_config) if args.app_config else root / "apps" / args.app / "deploy" / "config.toml"

    for label, path in [("Base config", base_path), ("App config", app_path)]:
        if not path.exists():
            print(f"Error: {label} not found: {path}", file=sys.stderr)
            sys.exit(1)

    base = load_toml(base_path)
    app_config = load_toml(app_path)
    cluster = base["ecs"]["cluster"]

    if args.env not in app_config and args.env != "global":
        print(
            f"Warning: No [{args.env}] section in {app_path}, using [global] only",
            file=sys.stderr,
        )

    config = resolve_config(base, app_config, args.env)
    render_env = args.env
    app_name = config["name"]
    secrets_name = config.get("secrets_name", app_name)
    secrets = resolve_secrets(
        app_config, args.env, secrets_name, base["ecs"]["secret_arn_prefix"],
    )

    names = compute_names(config, render_env, cluster)
    migrations = config.get("database_migrations", False)
    migration_command = config.get("migration_command")

    if migrations and not migration_command:
        print(
            f"Error: database_migrations is enabled for {app_name} but migration_command is not set",
            file=sys.stderr,
        )
        sys.exit(1)

    if args.metadata:
        print_metadata(
            names, app_name,
            desired_count=config.get("desired_count", 1),
            migrations=migrations,
            migration_command=migration_command,
        )
        return

    image_tag = args.image_tag or "latest"
    task_def = build_task_definition(base, config, names, render_env, image_tag, secrets)

    if args.dry_run:
        print(f"App:    {config['name']}", file=sys.stderr)
        print(f"Env:    {render_env}", file=sys.stderr)
        print(f"Family: {names['family']}", file=sys.stderr)
        print(f"Image:  {task_def['containerDefinitions'][0]['image']}", file=sys.stderr)
        print(f"CPU:    {task_def['cpu']}  Memory: {task_def['memory']}", file=sys.stderr)

        env_count = len(task_def["containerDefinitions"][0].get("environment", []))
        secret_count = len(task_def["containerDefinitions"][0].get("secrets", []))
        print(f"Env vars: {env_count}  Secrets: {secret_count}", file=sys.stderr)

    task_def_json = json.dumps(task_def, indent=2)

    if args.output:
        Path(args.output).write_text(task_def_json)
        print_metadata(
            names, app_name,
            taskdef_file=args.output,
            desired_count=config.get("desired_count", 1),
            migrations=migrations,
            migration_command=migration_command,
        )
    else:
        print(task_def_json)


if __name__ == "__main__":
    main()
