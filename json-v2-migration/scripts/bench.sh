#!/bin/zsh
# bench.sh OUTDIR ROUNDS COUNT BENCHTIME VARIANT...
# A VARIANT is NAME=TREEROOT[:GOEXPERIMENT]. For each round, for each module,
# for each variant: run the BenchmarkJSON benchmarks of that module's
# benchmark packages and append to OUTDIR/NAME/<module>.txt. Alternating
# variants inside a round spreads machine drift evenly over them.
S=${S:-$(cd "$(dirname "$0")/.." && pwd)/data}
ROOT=${ROOT:-$HOME/src/latere-ai}
out=$1; rounds=$2; count=$3; benchtime=$4; shift 4
variants=("$@")
mkdir -p $out
# module dir -> package list, from the benchmark file list
typeset -A pkgs
while read f; do
  d=$(dirname $f)
  m=$d
  while [ ! -f $ROOT/$m/go.mod ]; do m=$(dirname $m); done
  rel=${d#$m}; rel=./${rel#/}; [ "$rel" = "./" ] && rel=.
  pkgs[$m]="${pkgs[$m]} $rel"
done < $S/bench-files.txt
mods=(${(ko)pkgs})
{
  echo "start $(date -u +%FT%TZ)"
  sysctl -n machdep.cpu.brand_string hw.ncpu hw.memsize
  sw_vers 2>/dev/null | tr '\n' ' '; echo
  echo "rounds=$rounds count/round=$count benchtime=$benchtime"
  echo "variants: ${variants[*]}"
} >> $out/run.log
for r in $(seq 1 $rounds); do
  for m in $mods; do
    for v in $variants; do
      name=${v%%=*}; rest=${v#*=}; tree=${rest%%:*}; exp=""
      [[ $rest == *:* ]] && exp=${rest#*:}
      mkdir -p $out/$name
      f=$out/$name/$(echo $m | tr / _).txt
      echo "$(date -u +%FT%TZ) round=$r module=$m variant=$name load=$(sysctl -n vm.loadavg)" >> $out/run.log
      ( cd $tree/$m && GOEXPERIMENT=$exp go test -run '^$' -bench '^BenchmarkJSON' -benchmem \
          -count $count -benchtime $benchtime -timeout 120m ${=pkgs[$m]} >> $f 2>> $out/$name/$(echo $m | tr / _).err )
      echo "$(date -u +%FT%TZ) exit=$? module=$m variant=$name" >> $out/run.log
    done
  done
done
echo "end $(date -u +%FT%TZ)" >> $out/run.log
