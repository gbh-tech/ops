---
"@gbh-tech/ops": minor
---

`ops ecs vars -f dotenv` now writes the resolved environment variables to
`{apps_dir}/{app}/.env` by default instead of printing to stdout.

Use `-o -` to restore the previous stdout behavior, or `-o /path/.env` to
write to a custom path. Output is deterministically sorted so repeated runs
are idempotent.
