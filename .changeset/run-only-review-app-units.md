---
"@gbh-tech/ops": minor
---

A unit whose review app section sets `replicas = 0` is run-only: `ops reviewapp deploy` registers its task definition and creates no service, so `ops reviewapp run --pr <n> <unit>` can start it on demand. Use it for scheduled jobs that should not run in a loop.
