---
"@gbh-tech/ops": minor
---

Infer `--app-config` basenames and add `-c` short flag

- `--app-config server` now resolves to `apps/<app>/deploy/server.toml|yaml|yml` (or `deploy/server.toml|yaml|yml` in single-repo mode).
- Subpaths such as `--app-config worker/server` resolve inside `deploy/worker/`.
- A clear fatal error is shown when multiple config files with different extensions exist.
- `-c` is now accepted as a short flag for `--app-config` on `ecs`, `build`, and `push` commands.
