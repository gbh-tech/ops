---
"@gbh-tech/ops": minor
---

Add `gpu` to deploy config so ECS task definitions emit `resourceRequirements` type=GPU.

GPU workloads (for example vLLM on EC2 GPU capacity providers) can now reserve GPUs from `deploy.toml` / `deploy/config.toml`:

```toml
[production]
cpu    = 4096
memory = 14336
gpu    = 1
```

When `gpu > 0`, the container definition includes:

```json
"resourceRequirements": [{ "type": "GPU", "value": "1" }]
```

Omit the field or set `gpu = 0` for CPU-only tasks. Requires an EC2 GPU AMI and a GPU capacity provider; Fargate does not support this setting.
