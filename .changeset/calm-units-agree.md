---
"@gbh-tech/ops": minor
---

Add `ops drift`, which compares an environment with a manifest of image tags and lists the catalog units that do not run the image their entry names. It reads ECS services and Lambda functions and reports each unit as `in-sync`, `out-of-sync`, `missing` or `unreadable`; `--format ids` prints the IDs that are not in sync and `--exit-code` fails when a unit is not in sync. The new `lambda.function_name` setting tells ops how the functions of `lambda` units are named.
