---
"@gbh-tech/ops": minor
---

Apply the review app lifetime rules of RFC 0004 in `ops reviewapp gc`: destroy a review app 48 wall-clock hours after creation, or after 8 awake hours without requests on its `/*` unit (`reviewapps.idle_timeout`, `--idle-timeout`; this replaces `idle_pause`, and idle review apps are now destroyed instead of paused). Time asleep never counts as idle, because `wake` restarts the clock. gc comments on the PR an hour before whichever limit comes first, and again when it destroys, giving the reason. `pause` and `unpause` are now `sleep` and `wake`; the old names stay as aliases.
