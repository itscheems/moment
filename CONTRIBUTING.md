# Contributing

## Pull request delivery

Create one concrete issue for each independent feature or fix, and link its PR in both directions. One issue describes one independent delivery unit; it does not limit the repository to one open PR.

Independent tasks may develop, open PRs, receive review and run CI concurrently. Only when the owner explicitly requests serial delivery for a particular batch must the previous PR merge and its issue closure be confirmed before starting the next unit from updated `main`. Dependencies and integration risks may also require ordering within a task; they do not impose repository-wide serialization.

## Merge policy

Only ordinary Merge Commits are allowed. Preserve every commit in the pull request. Squash merging and rebase merging are forbidden through the web UI, CLI, API, and auto-merge. Never temporarily enable either method or bypass protections, including with administrator privileges.

The repository must keep `allow_merge_commit=true`, `allow_squash_merge=false`, and `allow_rebase_merge=false`. The `merge-policy` check is required on `main`, bound to GitHub Actions, with administrator enforcement retained. Force pushes and branch deletion remain disabled.

Merge method and branch synchronization are separate policies. Disabling squash/rebase does not require an up-to-date branch or `required_status_checks.strict=true`. The current settings check reads repository configuration; it does not test product integration. Its branch protection uses `strict=false`. Any future up-to-date requirement must have its own functional or integration-risk justification and preserve the merge-method restrictions. Resolve actual conflicts and satisfy all applicable checks before merging.

### Before merging: settings gate

Provide `GH_TOKEN` or `GITHUB_TOKEN` with repository read access and run `task merge:check`. GitHub's disabled merge methods enforce the allowed path; the required settings check detects configuration drift. Then use `gh pr merge --merge --match-head-commit <head-sha>`. Do not commit credentials. A settings check cannot predict whether an arbitrary future commit has the right topology.

The required workflow runs for PR creation, reopening, new commits, readiness and edits, including retargeting a PR to `main`; it also runs on pushes to `main` and manual dispatch. It needs only Node and Git, without installing unrelated build/security toolchains.

### After merging: merge-result audit

Run `task merge:audit -- --pr <number>`. The separate `merge-result-audit` workflow also audits a merged PR's `closed` event; manually dispatch it with a merged PR number to repeat an audit. Closing an unmerged PR does not request an audit.

A valid result has exactly two parents, with the recorded PR head as its second parent. Audit the PR's recorded `merge_commit_sha` and `head.sha`, never the latest `main` or its current tip. If A and B branch from the same base, A can merge first and B can then merge normally without updating B merely for this policy. Both audits remain valid after later main commits. Retaining each PR head retains all its ancestor commits.

This is a retrospective audit: failure reports an already existing violation; it cannot prevent, reject retroactively or undo that merge. Investigate and correct the cause. Never automatically roll back, rewrite history or excuse an invalid topology to make an audit pass. Historical topology audits do not depend on today's merge settings.

### GitHub API failures

Requests time out after 10 seconds per attempt, including response bodies, with at most three attempts. Only transient network failures, rate limits and temporary server failures retry, with bounded backoff and at most 60 seconds per wait. Respect `Retry-After` and rate-limit reset times; if GitHub requires a longer wait, fail and try again later instead of retrying early. Authorization/permission failures, missing resources or data, malformed success responses, known settings violations and invalid merge results fail clearly. Unknown is never treated as compliant.

### Merge Queue boundary

The current public repository is owned by a personal account and is ineligible for native Merge Queue under [GitHub's eligibility rules](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue). No queue is enabled or supported by these workflows. The checker explicitly rejects `merge_group` invocations rather than claiming queue support. Supporting a queue later requires separate authorization, eligibility, required checks on `merge_group`, and evidence that its actual merge products retain the PR heads under these audit rules. Do not relax parent/head validation or build a replacement queue speculatively.

### Regression verification

Run `task merge:test` (also part of `task ci`). It separates function assertions, real disposable local Git histories and real local HTTP fault tests. Local Git tests cover normal merges, real squash/rebase results, independently branched A/B delivery and later main advancement. HTTP tests cover retries, timeouts and terminal failures; workflow contract tests cover event declarations and routing. These tests are not real GitHub event/queue or branch-protection enforcement tests. Verify actual repository settings and hosted workflow results separately when delivering a policy change; never manufacture a batch of production PRs just to test history.
