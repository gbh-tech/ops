---
"@gbh-tech/ops": patch
---

`ops reviewapp` fixes from the first end-to-end deploy: the client prints every line of the controller's logs exactly once, including the first lines and the final error of a failed run; a failed deploy comments on the PR which step failed, with the error and the workflow run; a deploy that succeeds after a failed create comments that the review app is ready instead of updated; and controller messages say sleep, wake and asleep instead of pause and unpause.
