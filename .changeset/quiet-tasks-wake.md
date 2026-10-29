---
"@gbh-tech/ops": minor
---

Add `ops reviewapp deploy`, `destroy`, `sleep`, `wake`, `run` and `gc`. `deploy` creates the review app the first time and updates it after that. Outside the cluster they run in the review app controller task; with `--in-cluster` they create and remove the per-PR namespace, database, helper tasks, task definitions, target groups, listener rules and services.
