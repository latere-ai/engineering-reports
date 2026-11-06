#!/bin/zsh
# land.sh TAG REPO... : check, commit and push each repository's
# encoding/json/v2 migration. ("Land" here means push to main once the checks
# pass; the "gate" in the log words and variables is the full CI check suite.)
# Published as a record of what ran, not as a runnable tool: it needs
# data/files/REPO.txt, data/hold.txt and data/own-commits.txt, which are not
# in the data directory. For each repository it stages exactly the
# files listed in files/REPO.txt, runs the repository's full CI checks
# locally with lateregate (the check runner CI uses,
# github.com/latere-ai/ci-gate) and every module's whole go test ./..., then
# commits, rebases onto the remote main and pushes. One status line per
# repository goes to land-TAG.log; repositories listed in hold.txt are
# committed but not pushed.
S=${S:-$(cd "$(dirname "$0")/.." && pwd)/data}
ROOT=${ROOT:-$HOME/src/latere-ai}
# DOCKER_HOST may point at a Podman socket; a docker shim on PATH serves kind.
[ -n "$SHIM" ] && export PATH=$SHIM:$PATH
msg="json: move to encoding/json/v2"
tag=$1; shift
log=$S/land-$tag.log

publish() {
  git pull -q --rebase --autostash origin main || return 1
  git push -q origin HEAD:refs/heads/main || return 1
  return 0
}

modules() {
  git ls-files '*go.mod' | grep -vE '(^|/)(testdata|test/tasks|\.worktrees)/' | xargs -n1 dirname
}

for repo in "$@"; do
  cd $ROOT/$repo || { echo "$repo CDFAIL" >> $log; continue; }
  list=$S/files/$repo.txt
  [ -s $list ] || { echo "$repo NOFILES" >> $log; continue; }
  files=("${(@f)$(cat $list)}")
  # Commits on this checkout's main that are not on the remote and were not
  # written by this script belong to other work; pushing would publish them.
  git fetch -q origin
  foreign=$(git rev-list origin/main..HEAD | grep -vxF -f $S/own-commits.txt | head -1)
  case $repo in
    sdk|managed-agents) gate=(go -C internal/gate tool lateregate -C ../..) ;;
    *) gate=(go tool lateregate) ;;
  esac
  git add -A -- "${files[@]}"
  # One retry: a test suite that loses a port or a cluster to the machine's
  # load passes the second time, and the tests that passed are cached.
  # Only a run whose failures are all in the test, end-to-end or image
  # checks is retried; a lint, license, modernize or coverage-floor finding
  # fails the same way twice.
  if ! "${gate[@]}" > $S/gate-$tag-$repo.log 2>&1 &&
     { ! grep -qE 'gates? failed: ' $S/gate-$tag-$repo.log ||
       grep -E 'gates? failed: ' $S/gate-$tag-$repo.log | sed 's/.*failed: //' | tr ',' '\n' | tr -d ' ' | grep -qvxE 'suite|e2e|image' ||
       grep -q 'coverage is under the floor' $S/gate-$tag-$repo.log ||
       ! "${gate[@]}" > $S/gate-$tag-$repo.retry.log 2>&1; }; then
    echo "$repo GATEFAIL see gate-$tag-$repo.log and .retry.log" >> $log
    git reset -q -- "${files[@]}"
    continue
  fi
  tfail=""
  for m in $(modules); do
    tlog=$S/test-$tag-$repo-${m//\//_}.log
    if ! (cd $m && go test ./... > $tlog 2>&1); then
      # A module that only holds tool requirements has no packages to test.
      grep -q 'matched no packages' $tlog || tfail="$tfail $m"
    fi
  done
  if [ -n "$tfail" ]; then
    echo "$repo TESTFAIL in$tfail see test-$tag-$repo-*.log" >> $log
    git reset -q -- "${files[@]}"
    continue
  fi
  if ! git commit -q -m "$msg" -- "${files[@]}" > $S/commit-$tag-$repo.log 2>&1; then
    echo "$repo COMMITFAIL see commit-$tag-$repo.log" >> $log
    git reset -q -- "${files[@]}"
    continue
  fi
  git rev-parse HEAD >> $S/own-commits.txt
  if grep -q "^$repo " $S/hold.txt; then
    echo "$repo COMMITTED-HELD $(git rev-parse HEAD)" >> $log
    continue
  fi
  if [ -n "$foreign" ]; then
    echo "$repo COMMITTED-HELD $(git rev-parse HEAD): unpushed commit $foreign not written by this script" >> $log
    continue
  fi
  ok=0
  for i in 1 2 3; do
    if publish >> $S/commit-$tag-$repo.log 2>&1; then ok=1; break; fi
    sleep $((i * 10))
  done
  if [ $ok = 1 ]; then
    echo "$repo PUSHED $(git rev-parse HEAD)" >> $log
  else
    echo "$repo PUSHFAIL (committed locally $(git rev-parse --short HEAD)) see commit-$tag-$repo.log" >> $log
  fi
done
echo "DONE" >> $log
