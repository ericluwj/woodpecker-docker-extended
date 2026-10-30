# Release policy

- Keep manual releases on `main` and retain native AMD64/ARM64 verification gates.
- Derive the upstream Woodpecker version from `go.mod`.
- Use `<woodpecker-version>-<revision>` for numbered GitHub Releases, Git tags,
  Docker Hub tags and GHCR tags. Never overwrite or reuse a numbered revision,
  including a reservation left by a failed release.
- Use `<woodpecker-version>-latest` only as a moving container/Git tag alias.
  Do not attach a GitHub Release to it: immutable releases lock their tags.
- Publish numbered images and the GitHub Release before promoting aliases.
- Do not restore unversioned `latest` or `sha-*` publication.
- Test release orchestration without remote mutations using
  `python3 -m unittest discover -s .github -p test_release.py`.
- Never run `git push`; leave publication and root infrastructure commands to
  the user. Do not create project folders or worktrees without explicit approval.
