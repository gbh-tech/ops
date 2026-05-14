---
"@gbh-tech/ops": patch
---

Add unit tests for app, config, ecs, and utils packages

Adds test coverage for `pkg/app` (config loading, secret normalization, build args), `pkg/config` (provider/registry inference, path resolution), `pkg/ecs` (config merging, secret resolution, health check validation, port validation), and `pkg/utils` (display helpers, git ticket ID extraction, registry URL construction).
