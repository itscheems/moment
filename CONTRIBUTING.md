# Contributing

## Pull request delivery

Create one concrete issue for each independent feature or fix, and link its PR in both directions. Complete and confirm one PR merge and issue closure before creating the next issue/PR pair from updated `main`.

## Merge policy

Only ordinary merge commits are allowed. Preserve every commit in the pull request. Squash merging and rebase merging are forbidden through the web UI, CLI, API, and auto-merge. Never temporarily enable either method or bypass the policy check.

The repository must keep `allow_merge_commit=true`, `allow_squash_merge=false`, and `allow_rebase_merge=false`. The `merge-policy` check is required on `main`, including for administrators.

Before merging, provide `GH_TOKEN` or `GITHUB_TOKEN` with repository read access and run `task merge:check`. Then use `gh pr merge --merge --match-head-commit <head-sha>`. Do not commit credentials.

After merging, run `task merge:check -- --merged-pr <number>`. A valid merge commit has two parents, with the original PR head retained as its second parent. CI checks the same contract when a PR closes.

Run the offline policy regression tests with `task merge:test`; they are also part of `task ci`.
