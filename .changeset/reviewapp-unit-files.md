---
"@gbh-tech/ops": minor
---

A unit's review app section can declare `files`: each entry maps an absolute container path to an object key under the review app's data prefix (`s3://<data bucket>/pr-<n>/<key>`). `ops reviewapp deploy` adds an init container that copies the objects to a task volume, mounted read-only at each file's directory, and the unit's container starts only after the copy succeeds. `[global]` may not set `files`, and `ops ecs` ignores it.
