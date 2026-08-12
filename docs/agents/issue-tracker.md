# Issue tracker: GitHub

Issues and specs for this repository live in GitHub Issues. Use the `gh` CLI for all operations.

## Conventions

- Create: `gh issue create --title "..." --body "..."`
- Read: `gh issue view <number> --comments`
- List: `gh issue list`
- Comment: `gh issue comment <number> --body "..."`
- Label: `gh issue edit <number> --add-label "..."` or `--remove-label "..."`
- Close: `gh issue close <number> --comment "..."`

Infer the repository from `git remote -v`; `gh` handles this automatically inside the clone.

## Implementation delivery gate

For work implementing an issue:

- Push the implementation branch and open a pull request against `main`.
- Include `Closes #<issue>` in the pull request body.
- End the implementation session with the issue open and the pull request unmerged, then report the pull request URL for human review.
- Issue closure belongs to GitHub's pull-request merge automation; implementation sessions do not run `gh issue close` or merge their own pull requests.

## Pull requests as a triage surface

**PRs as a request surface: no.**

## Skill conventions

- When a skill says “publish to the issue tracker,” create a GitHub issue.
- When a skill says “fetch the relevant ticket,” run `gh issue view <number> --comments`.
- GitHub shares one number space across issues and pull requests. Resolve ambiguous references with `gh pr view <number>`, then fall back to `gh issue view <number>`.
