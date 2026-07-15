## Summary

Adds `{env}` template support to `ecs.cluster` so a single config value like `lighthouse-platform-{env}` resolves to the correct ECS cluster per `--env`.

## Changes

- Add `ECSConfig.ResolvedCluster(env)` to expand `{env}` in cluster names
- Wire cluster resolution through all ECS commands via `buildBaseConfig` / `loadECSCtx`
- Update `.ops/config.yaml` example to `ecs.cluster: lighthouse-platform-{env}`
- Add unit tests for placeholder expansion and passthrough behavior
- Document templated cluster names in `README.md`
- Add minor changeset for `@gbh-tech/ops`

## Test plan

```bash
# Unit tests
go test -run TestECSConfigResolvedCluster ./pkg/config

# Build the CLI
go build -o ops

# Confirm config still loads the template (raw value)
./ops config --deployment ecs

# Leopard: verify cluster resolves to lighthouse-platform-leopard
./ops ecs render --env leopard --app <app> --tag leopard
./ops ecs status --env leopard --app <app>
```

Replace `<app>` with the target ECS app. `render` is a safe dry-run; `status` confirms the resolved cluster against AWS (requires leopard credentials).
