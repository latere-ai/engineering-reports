#!/bin/zsh
# census.sh SHAFILE : per "repo sha" line, measure the repo at that commit:
# Go modules, Go files, Go lines, files importing encoding/json (v1),
# files importing encoding/json/v2 or jsontext, and json.X call sites.
# Prints TSV with a header.
ROOT=${ROOT:-$HOME/src/latere-ai}
print "repo\tsha\tmodules\tgo_files\tgo_lines\tv1_files\tv2_files\tcall_sites"
while read repo sha; do
  [ -z "$repo" ] && continue
  g=(git -C $ROOT/$repo)
  files=("${(@f)$($g ls-tree -r --name-only $sha | grep '\.go$')}")
  mods=$($g ls-tree -r --name-only $sha | grep -c '\(^\|/\)go\.mod$')
  nfiles=${#files}
  lines=$($g grep -h -c '' $sha -- '*.go' | awk '{s+=$1} END {print s+0}')
  v1=$($g grep -l -E '^[[:space:]]*(import[[:space:]]+)?([A-Za-z_.]+[[:space:]]+)?"encoding/json"[[:space:]]*$' $sha -- '*.go' | wc -l | tr -d ' ')
  v2=$($g grep -l -E '"encoding/json/(v2|jsontext)"' $sha -- '*.go' | wc -l | tr -d ' ')
  calls=$($g grep -h -o -E '(^|[^A-Za-z0-9_])json(v1|v2)?\.(Marshal|MarshalIndent|Unmarshal|NewEncoder|NewDecoder|Valid|Compact|Indent|HTMLEscape|MarshalWrite|MarshalEncode|UnmarshalRead|UnmarshalDecode)\(' $sha -- '*.go' | wc -l | tr -d ' ')
  print "$repo\t$sha\t$mods\t$nfiles\t$lines\t$v1\t$v2\t$calls"
done < $1
