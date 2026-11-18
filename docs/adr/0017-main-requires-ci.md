# 17. The main branch requires CI, and the changelog is pushed with a deploy key

- Status: Accepted, November 18, 2026

## Context

Nothing on GitHub stopped a commit that failed CI, or never ran it, from
reaching `main`. The Dependabot auto-merge job waited for the checks itself,
which protects only the merges that job makes.

One commit reaches `main` with no check at all: after a full release, the
release workflow's `changelog` job commits the regenerated `CHANGELOG.md`
and pushes it. GitHub's rule for required checks says that "commits must
first be pushed to another ref where the checks pass", so a rule on `main`
refuses that push, and a push made with the workflow token is not let
through a rule.

## Options considered

1. **No rule.** Each merging job waits for the checks itself. A direct push
   and a merge by hand stay unchecked.
2. **A rule, and the changelog through a pull request.** A pull request
   opened with the workflow token starts no workflow, so its required check
   never reports and it never merges.
3. **A rule, and no changelog commit.** The release notes would be the only
   record; `CHANGELOG.md` in the repository is what `README.md` points to.
4. **A rule, and a personal access token on its bypass list.** The token
   acts as a person, reaches every repository that person's token covers,
   and expires.
5. **A rule, and a GitHub App on its bypass list**
   (`actions/create-github-app-token`). Its tokens last an hour and name
   their permissions, which suits an organization with many repositories.
   For one repository and one push it means registering and installing an
   App and still storing its private key as a secret.
6. **A rule, and a deploy key on its bypass list.** A deploy key is an SSH
   key that "grants access to a single repository" and belongs to no
   account. Rulesets have taken deploy keys as a bypass actor since April
   2024.

## Decision

Option 6.

- **One check.** `ci.yml` ends in an `all-checks` job that needs every other
  job, always runs and fails unless each of them succeeded. The rule names
  that one check, so adding or renaming a CI job changes no rule.
- **The rule.** A repository ruleset on the default branch holds one rule,
  `required_status_checks`, for the context `all-checks` reported by GitHub
  Actions. Its bypass list holds one actor, `DeployKey`, with mode `always`.
  It does not ask a branch to be up to date before merging.
- **The key.** The repository has one deploy key with write access. Its
  private half is the Actions secret `CHANGELOG_DEPLOY_KEY`, and no copy is
  kept elsewhere. The `changelog` job checks out with it (`ssh-key`) and has
  read-only token permissions.
- **Repository secret, not an environment secret.** An environment limited
  to `v*` tags would keep the secret from a workflow on any other ref. Only
  an account with write access can add such a workflow, runs for pull
  requests from forks and from Dependabot get no repository secrets, and an
  environment adds a deployment record to every release, so the secret
  stays a repository secret.

## Consequences

- A commit reaches `main` only after `all-checks` passed for it: push it to
  a `feat/**` branch or a pull request, wait for CI, then fast-forward or
  merge. A direct push of an unchecked commit is refused for every account,
  the owner's included.
- The `DeployKey` actor has no id, so the rule lets every deploy key with
  write access through. The repository keeps exactly one.
- A deploy key does not expire. To replace it, add a new key, set the secret
  to its private half and delete the old key.
- The changelog push starts a CI run on `main`, which a push with the
  workflow token did not.
- The `all-checks` check does not exist until the jobs it needs have
  finished. Until then a pull request reports no required check, so the
  Dependabot auto-merge job is expected to wait for the checks itself and
  then merge and delete the branch; the rule is what refuses a merge made
  any other way.
- The first run of the `changelog` job with the key is the next full
  release. Without the secret, the checkout falls back to the workflow token
  and the push is refused, with `CHANGELOG.md` on `main` unchanged.

## Sources

- GitHub Docs, REST API endpoints for rules:
  <https://docs.github.com/en/rest/repos/rules> (`DeployKey` actor with a
  null `actor_id`; "commits must first be pushed to another ref where the
  checks pass").
- GitHub Changelog, April 30, 2024, "Deploy keys are now supported as a
  bypass actor in repository rules":
  <https://github.blog/changelog/2024-04-30-repository-updates-april-30th-2024/>
- GitHub Docs, Managing deploy keys:
  <https://docs.github.com/en/authentication/connecting-to-github-with-ssh/managing-deploy-keys>
- GitHub Docs, Managing environments for deployment:
  <https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/manage-environments>
- GitHub Community discussion 43460, a bypass actor and required checks:
  <https://github.com/orgs/community/discussions/43460>
