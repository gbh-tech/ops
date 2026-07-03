---
"@gbh-tech/ops": minor
---

Fix CLI shorthand flag conflicts that panicked at command initialization

- Move global `--app-config` short flag from `-c` to `-g` to avoid clashes with subcommand flags.
- Restore `-f` shorthand on `ecs vars --format`.
- Split `ecs db-proxy` into its own subcommand instead of a `port-forward` alias.
- Add a regression test that detects persistent/local shorthand conflicts across the command tree.
