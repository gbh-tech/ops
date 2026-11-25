---
"@gbh-tech/ops": patch
---

`ops reviewapp` sets `S3_KEY_PREFIX` to `pr-<n>` instead of `pr-<n>/`. Apps join the prefix and the key with `/` themselves, and a client that checks the prefix refused the trailing slash, so units that use S3 failed to start.
