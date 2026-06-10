---
"@gbh-tech/ops": major
---

Make ECS service naming explicit: service operations now target `name` exactly by default, and `append_environment = true` opts in to the legacy `{name}-{env}` service target.
