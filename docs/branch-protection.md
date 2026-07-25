# Branch protection: `main` and `develop`

This document describes the rules we want on the Mortise GitHub repo's two
permanent branches. The CI workflow at `.github/workflows/ci.yml` is the
_spec_ — these rules ensure PRs cannot merge unless CI passes.

> **Status:** Applied by Azeem in the GitHub UI once the remote is live.
> Re-check after link-up: <https://github.com/AzeemWorsdorfer/Mortise/settings/branches>

## What the rules do

For both `main` and `develop`:

| Rule                                          | Value                         |
| --------------------------------------------- | ----------------------------- |
| Require a pull request before merging         | ✅ on                          |
| Required approvals                            | 1 (configurable up to 2)      |
| Dismiss stale pull request approvals on push  | ✅ on                          |
| Require review from Code Owners               | off (added ad-hoc per repo)   |
| Restrict who can dismiss pull request reviews | admins only                   |
| Allow specified actors to bypass              | **none** — no admins, no one  |
| Require status checks to pass before merging  | ✅ on                          |
| Require branches to be up to date before merging | ✅ on                       |
| Require status checks from `ci` workflow jobs | `go-lint`, `ts-lint`, `proto-check` |
| Require linear history                        | ✅ on                          |
| Require deployable history (no force pushes)  | ✅ on                          |
| Block force pushes                            | ✅ on                          |
| Block branch deletions                        | ✅ on                          |
| Allow auto-merge                              | ✅ on                          |

The "no bypass" line is the important one — admins cannot push directly to
either branch during normal flow. Emergency hotfixes use the `gh` CLI override
described below.

## How to apply (GitHub UI)

Repeat for `main` and `develop`:

1. Open <https://github.com/AzeemWorsdorfer/Mortise/settings/branches>.
2. Click **Add branch protection rule**.
3. Branch name pattern: `main` (then repeat for `develop`).
4. Check **Require a pull request before merging**.
5. Set **Required approvals: 1**.
6. Check **Dismiss stale pull request approvals when new commits are pushed**.
7. Check **Require status checks to pass before merging**.
8. In the search box, type `ci` and select these three jobs:
   - `go-lint`
   - `ts-lint`
   - `proto-check`
9. Check **Require branches to be up to date before merging**.
10. Check **Require linear history**.
11. Check **Do not allow force pushes**.
12. Check **Do not allow deletions**.
13. **Allow specified actors to bypass** — leave empty.
14. Click **Create**.

## Why no "allow admins to bypass"?

The point of the two-branch layout is that `develop` is the working branch
and `main` is the release branch. If administrators can bypass, the
attestation that "CI is green" is weakened — and the foundation ticket's
acceptance criterion is "no direct pushes to either branch". We can re-evaluate
in a later ADR if the workflow proves too rigid.

## Emergency hotfix (if GitHub UI is unavailable)

If the GitHub settings UI is locked and we need to merge a hotfix to `main`
_without_ going through CI, the only sanctioned path is:

```bash
# Locally (requires gh auth + repo admin)
gh api -X PATCH \
  -H "Accept: application/vnd.github+json" \
  /repos/AzeemWorsdorfer/Mortise/branches/main/protection \
  --input .github/branch-protection-main.json

# Then merge the PR, then re-apply the rule.
```

The `.github/branch-protection-*.json` snapshots are checked into the repo
so this emergency flow is reproducible.

## Files

- `.github/workflows/ci.yml` — the CI jobs that the rules reference.
- `.github/branch-protection-main.json` — snapshot of the `main` rule (optional, for emergency restore).
- `.github/branch-protection-develop.json` — snapshot of the `develop` rule.
