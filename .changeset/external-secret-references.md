---
"@gbh-tech/ops": minor
---

Add support for external Secrets Manager secret references in deploy config secrets.

Per-service deploy configs can now reference a secret that lives OUTSIDE the service's implicit shared/env path using an inline-table entry:

```toml
[stage.secrets]
DATABASE_URL   = "db_url"                                                 # existing
CLAUDE_API_KEY = { secret = "anthropic/stage", key = "CLAUDE_API_KEY" }  # new external
```

The `secret` field accepts either a bare name (appended to the cluster ARN prefix) or a full `arn:...` ARN. The `key` field is the JSON key within the Secrets Manager secret and defaults to the env-var name when omitted.

This is supported for both `[global.secrets]` and `[<env>.secrets]` (runtime ECS secrets only). Build-time secrets (`build_secrets`) are unchanged and continue to reject external references.

The ECS execution role must be granted `secretsmanager:GetSecretValue` on the external secret ARN via Terraform for this to work at runtime.
