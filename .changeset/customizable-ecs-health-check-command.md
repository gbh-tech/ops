---
"@gbh-tech/ops": minor
---

Add `command` field to `container_health_check` for customizable ECS health checks

Containers that do not have `curl` installed can now specify their own health check command directly in the app config instead of relying on the hardcoded `curl` fallback.

```toml
[global.container_health_check]
command = ["CMD-SHELL", "wget -q -O /dev/null http://localhost:8080/health || exit 1"]

# Or exec form for a binary check (no shell required)
command = ["CMD", "/bin/healthcheck"]
```

The `command` field accepts the same format as the ECS `HealthCheck.Command` API: the first element must be `"CMD-SHELL"` (shell form) or `"CMD"` (exec form). An invalid prefix is now rejected at config load time with a clear error message before any AWS call is made.

Previously, setting a custom command on a portless container (workers, queue consumers) was silently ignored because the health check guard required both `health_check_path` and a port — conditions only needed for the curl fallback. That bug is fixed.
