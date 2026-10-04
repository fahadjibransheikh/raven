---
name: release
description: Cut a new Raven desktop release or write the release notes for one.
  Use when the user asks to release, ship, publish, cut or tag a new version of
  Raven, bump the app version, or fill in / write / update release notes.
---

# Releasing Raven

CI does the building and publishing. Your jobs are the version bump, the tag,
watching the run, checking the result, and writing the release notes.

**Always pass `-R fahadjibransheikh/raven` to `gh`.** This repo is a fork, and
`gh` without `-R` targets the upstream repo (cristianadrielbraun/gofer).

## 1. Cut the release

Skip this section if the user only wants notes for an existing release.

1. `git pull --rebase origin master`. CI commits the Homebrew cask bump to
   master after every release, so local master is often behind.
2. The working tree must be clean and every change meant for this release
   committed. If not, stop and ask.
3. Pick the version. Default to a patch bump (0.1.3 → 0.1.4). Use a minor
   bump only if the user asks. The previous version is
   `git describe --tags --abbrev=0`.
4. Change `version` in `tauri-wrapper/src-tauri/Cargo.toml` only.
   `tauri.conf.json` has no version on purpose. Run
   `(cd tauri-wrapper/src-tauri && cargo check -q)` so `Cargo.lock` picks up
   the new version.
5. Commit `tauri-wrapper/src-tauri/Cargo.toml` and `Cargo.lock` with the
   message `Bump desktop app version to X.Y.Z`. Then run
   `git push origin master`, `git tag vX.Y.Z` and `git push origin vX.Y.Z`.
6. Watch the run. It takes about 12 minutes, so run this in the background:
   `gh run list -R fahadjibransheikh/raven -w build-desktop.yml -L 1` gives
   the run ID, then poll `gh run view <id> -R ... --json status,conclusion,jobs`
   until it completes. `gh run watch` drops on network blips, so don't rely
   on it.
7. If a job failed, read its log with `gh run view <id> -R ... --log-failed`
   and report it. The release stays a draft, so users see nothing. Don't
   delete the tag or re-tag without asking.

## 2. Check the release

- `gh release list -R ...`: the new release is **Latest** and not a draft.
- Assets: the `.dmg`, `-setup.exe`, `.msi`, `.deb`, `.rpm`, `.AppImage` and
  `Raven_X.Y.Z_aarch64.app.tar.gz`, each with a `.sig` where the updater needs one,
  plus `latest.json`.
- `curl -sL https://github.com/fahadjibransheikh/raven/releases/latest/download/latest.json`
  shows the new `version`, with `darwin-aarch64`, `windows-x86_64` and
  `linux-x86_64` under `platforms`.
- `git pull` and confirm `Casks/raven.rb` has the new version, committed by
  CI as `Update Homebrew cask to X.Y.Z`.

## 3. Write the release notes

CI publishes the release with a fixed body: Downloads, Homebrew, the
Gatekeeper note, the update note, and `## Changes` followed by the
placeholder `_Release notes to follow._`. Replace only the placeholder. Leave
the rest of the body as it is.

1. Collect what changed:
   `git log --no-merges --format='%h %s%n%b' vPREV..vX.Y.Z`. Read the commit
   bodies, not only the subjects. If a commit is unclear, read its diff.
2. Keep only what a user of the app would notice. Leave out version bumps,
   CI or workflow changes, README changes, refactors, and the Homebrew cask
   bumps, unless they change how people install or use Raven.
3. Write for people who use Raven, not for developers:
   - Group under `### New` and `### Fixes`, and add `### Changes` for
     behavior changes that are neither. Leave out empty groups.
   - One bullet per change. For fixes, describe what the user saw before and
     what happens now, for example "Reply stopped working after sending a
     message. It now works every time."
   - Plain words. No file names, function names, crate or plugin names, or
     commit hashes.
   - Say which platform when a change doesn't apply to all of them.
   - Mention any one-time action a user must take, such as installing by hand.
4. Apply it:
   `gh release view vX.Y.Z -R ... --json body -q .body > body.md` (in the
   scratchpad), replace `_Release notes to follow._` with the new text,
   `gh release edit vX.Y.Z -R ... --notes-file body.md`, then view the body
   again to confirm.
5. Show the user the Changes section you published. They can ask for edits.

For an existing release, compare against the tag before it
(`git tag --sort=-v:refname`).
