#!/bin/zsh
# trees.sh DEST SHAFILE : export each "repo sha" line of SHAFILE as a clean
# tree under DEST/<repo>, so measurements never see a working tree that
# another session is editing.
ROOT=${ROOT:-$HOME/src/latere-ai}
dest=$1; shas=$2
mkdir -p $dest
while read repo sha; do
  [ -z "$repo" ] && continue
  if [ -d $dest/$repo ]; then echo "$repo exists, skipped"; continue; fi
  mkdir -p $dest/$repo
  git -C $ROOT/$repo archive --format=tar $sha | tar -x -C $dest/$repo || { echo "$repo FAILED"; continue; }
  echo "$repo $sha"
done < $shas
