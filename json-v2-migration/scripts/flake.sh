#!/bin/zsh
# flake.sh TREE SHAFILE OUT : per "repo sha" line, rerun three times every
# package whose test files that commit changed, in the clean tree
# TREE/<repo>. A test that compares map-built JSON byte for byte passes a
# single run often enough to land, so one green run proves nothing about
# map order. Writes OUT/<repo>-<module>.log and OUT/summary.txt, one line
# per module: "ok (N pkgs)" or the first failing tests.
#
# A failure here is a lead, not a verdict: a test that cannot repeat in one
# process (a fixed database key, a package-level counter, a process limit
# it leaves set) fails -count=3 as well. Rerun such a test in the tree of
# the parent commit with -count=3, and in TREE as separate processes.
ROOT=${ROOT:-$HOME/src/latere-ai}
tree=$1; shas=$2; out=$3
mkdir -p $out; : > $out/summary.txt
while read repo sha; do
  [ -z "$repo" ] && continue
  typeset -A pk; pk=()
  for f in $(git -C $ROOT/$repo diff --name-only $sha~1 $sha -- '*_test.go'); do
    d=$(dirname $f); m=$d
    while [ "$m" != "." ] && [ ! -f $tree/$repo/$m/go.mod ]; do m=$(dirname $m); done
    [ -f $tree/$repo/$m/go.mod ] || continue
    rel=${d#$m}; rel=./${rel#/}; [ "$rel" = "./" ] && rel=.
    [[ " ${pk[$m]} " == *" $rel "* ]] || pk[$m]="${pk[$m]} $rel"
  done
  for m in ${(k)pk}; do
    log=$out/$repo-${m//\//_}.log
    if (cd $tree/$repo/$m && go test -count=3 -short ${=pk[$m]} > $log 2>&1); then
      echo "$repo $m ok ($(echo ${pk[$m]} | wc -w | tr -d ' ') pkgs)" >> $out/summary.txt
    else
      echo "$repo $m FAIL: $(grep -E -- '--- FAIL|panic: test timed out' $log | sort -u | head -5 | tr '\n' ' ')" >> $out/summary.txt
    fi
  done
done < $shas
