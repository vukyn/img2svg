---
name: commits-always-via-pr
description: "All commits to main go through a PR — never direct push to main, any repo, any size (user rule 2026-06-07)"
metadata: 
  node_type: memory
  type: feedback
  originSessionId: f284e2fb-f9bb-4b32-8234-a5e66a2962b6
---

User rule (set 2026-06-07 after a housekeeping commit was pushed directly to isme main): every change lands on main via branch + PR, no exceptions — including trivial chores, doc moves, build-asset refreshes. Applies to all platform repos (isme, medioa2, rainy, kuery, memz, …).

**Why:** Review gate + audit trail; the committer agent previously had discretion to push direct when "repo convention allowed", which the user rejected.

**How to apply:** Always instruct the committer agent: branch from main → commit → push → open PR; never grant direct-push discretion. Merge only after verifying staged files ([[verify-committer-staged-files]]) and with user approval unless they pre-authorized merging in the same request. kuery releases too: commit via PR, then tag after merge.

**⚠️ The merge strategy is SQUASH, and I got this wrong on 2026-09-05.** Measured across main: rainy **327** squash-style commits (`subject (#N)`) vs **1** merge commit, gardener 114 vs 1, medioa2 93 vs 1, isme 80 vs 7, hexarena 227 vs 43 — and most of hexarena's 43 are from one session where I merged ~12 PRs by hand with `gh pr merge --merge`. `.claude/agents/committer.md` and `/pet-commit` both say `--squash`; **they are right and I was the deviation.** Use `gh pr merge <n> --squash --delete-branch` unless the user asks otherwise.
