---
"@gbh-tech/ops": patch
---

Reject `container_health_check.command` slices with no argument

A command like `["CMD"]` or `["CMD-SHELL"]` was previously accepted but would fail at AWS task-definition registration. `validateHealthCheckCommand` now returns a clear error when the slice is shorter than 2 elements, catching the misconfiguration before any AWS call is made.
