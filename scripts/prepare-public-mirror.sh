#!/usr/bin/env bash
# Prepare a public mirror of Abhed: clone the working repo into a scratch
# directory, strip deploy/.db-password from all of history in that clone, and
# print the instructions to push it to a new public remote.
#
# This script NEVER touches the working repository at SOURCE_REPO. It only
# reads from it (via `git clone`) and operates on a throwaway copy. If you
# want to double check that promise, read this file: there is no `git`
# invocation below whose working directory is SOURCE_REPO, and no `--hard`,
# `push --force` or history rewrite runs anywhere but inside SCRATCH_DIR.
set -euo pipefail

SOURCE_REPO="${SOURCE_REPO:-/Users/yuvrajsingh/titan}"
SCRATCH_DIR="${SCRATCH_DIR:-$(mktemp -d /tmp/abhed-public-mirror.XXXXXX)}"
SECRET_PATH="deploy/.db-password"

echo "==> Source (read-only):   $SOURCE_REPO"
echo "==> Scratch (disposable): $SCRATCH_DIR"
echo

if [ ! -d "$SOURCE_REPO/.git" ]; then
  echo "error: $SOURCE_REPO is not a git repository" >&2
  exit 1
fi

# ---------------------------------------------------------------- clone ---
# A fresh clone, not a copy: filter-repo rewrites refs and history, and doing
# that anywhere near the working repo risks the operator confusing the two.
# Cloning also guarantees the working repo's reflog, stash, and any
# uncommitted state are untouched — clone only ever reads committed history.
echo "==> Cloning $SOURCE_REPO into $SCRATCH_DIR"
git clone "$SOURCE_REPO" "$SCRATCH_DIR"
cd "$SCRATCH_DIR"

# ---------------------------------------------------------- verify secret --
echo
echo "==> Checking whether $SECRET_PATH is in history before stripping"
if git log --all --oneline -- "$SECRET_PATH" | head -1 >/dev/null 2>&1 && \
   [ -n "$(git log --all --oneline -- "$SECRET_PATH")" ]; then
  echo "    found in history — will strip it:"
  git log --all --oneline -- "$SECRET_PATH"
else
  echo "    not found in history — nothing to strip, but continuing so the"
  echo "    verification step below still runs and confirms it stays gone."
fi

# --------------------------------------------------------- filter-repo ----
if command -v git-filter-repo >/dev/null 2>&1; then
  echo
  echo "==> Removing $SECRET_PATH from all history with git-filter-repo"
  # --force: this is a fresh clone made specifically to be rewritten, not the
  # operator's working copy, so filter-repo's "did you mean to do this to
  # your only copy" guard does not apply here.
  git filter-repo --force --invert-paths --path "$SECRET_PATH"
else
  cat <<'EOF'

==> git-filter-repo is not installed.

    Install it, then re-run this script:

        brew install git-filter-repo

    (git-filter-repo is the tool the Git project itself recommends over
    `filter-branch`, which is slow and easy to get wrong for exactly this
    kind of history rewrite.)

EOF
  exit 1
fi

# ------------------------------------------------------------- verify -----
echo
echo "==> Verifying $SECRET_PATH is gone from all history"
remaining="$(git log --all -- "$SECRET_PATH")"
if [ -n "$remaining" ]; then
  echo "error: $SECRET_PATH is still reachable in history:" >&2
  echo "$remaining" >&2
  exit 1
fi
echo "    confirmed: git log --all -- $SECRET_PATH is empty"

# ------------------------------------------------------------- gitleaks ---
echo
if command -v gitleaks >/dev/null 2>&1; then
  echo "==> Running gitleaks against the full history of the scratch clone"
  gitleaks detect --source "$SCRATCH_DIR" --no-git -v || true
  gitleaks detect --source "$SCRATCH_DIR" -v || \
    echo "    gitleaks reported findings above — review them before pushing."
else
  cat <<'EOF'
==> gitleaks not found — skipping the secret scan.

    Install it and re-run to get one:

        brew install gitleaks

EOF
fi

# ---------------------------------------------------------- push instructions
cat <<EOF

==> Done. The scratch clone at:

        $SCRATCH_DIR

    has had $SECRET_PATH removed from every commit, and the working
    repository at $SOURCE_REPO was never modified.

    Next steps to publish it:

        1. Create a new, empty public repository (e.g. on GitHub):
               gh repo create yuvrajsingh/abhed --public --source="$SCRATCH_DIR" --remote=public-origin

           or manually add the remote once the empty repo exists:
               cd "$SCRATCH_DIR"
               git remote add public-origin git@github.com:yuvrajsingh/abhed.git

        2. Push every branch and tag:
               cd "$SCRATCH_DIR"
               git push public-origin --all
               git push public-origin --tags

        3. Double-check on the remote's own web UI that $SECRET_PATH does not
           appear anywhere — a rewritten history that was already pushed
           somewhere else (a fork, a CI cache, a previous push attempt) can
           still leak it via that other copy.

        4. Rotate the value that was in $SECRET_PATH regardless of the
           outcome above. A password that was ever committed should be
           treated as disclosed — see docs/trust/incident-response.md for
           the rotation step (deploy/run.sh regenerates it once the file is
           removed and the script is re-run).

    This scratch clone is disposable. Remove it once the push above
    succeeds:

        rm -rf "$SCRATCH_DIR"
EOF
