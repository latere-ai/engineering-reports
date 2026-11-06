#!/bin/zsh
# final-shas.sh : for every repo with a benchmark commit, find the migration
# commit on origin/main and write "repo sha" lines for its parent
# (shas-before.txt) and for itself (shas-after.txt). The pair isolates what
# the migration commit changed, whatever else was merged after the benchmarks.
S=${S:-$(cd "$(dirname "$0")/.." && pwd)/data}
ROOT=${ROOT:-$HOME/src/latere-ai}
: > $S/shas-before.txt; : > $S/shas-after.txt
while read repo bench; do
  [ -z "$repo" ] && continue
  git -C $ROOT/$repo fetch -q origin
  sha=$(git -C $ROOT/$repo log --format=%H --grep='^json: move to encoding/json/v2$' $bench..origin/main | tail -1)
  if [ -z "$sha" ]; then echo "$repo MISSING"; continue; fi
  echo "$repo $(git -C $ROOT/$repo rev-parse $sha^)" >> $S/shas-before.txt
  echo "$repo $sha" >> $S/shas-after.txt
  echo "$repo $(git -C $ROOT/$repo rev-list --count $bench..$sha^) commits between benchmarks and migration"
done < $S/shas-benchmarks.txt
