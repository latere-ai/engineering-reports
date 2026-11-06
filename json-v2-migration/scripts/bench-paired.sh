#!/bin/zsh
# bench-paired.sh OUTDIR ROUNDS BENCHTIME VARIANT...
# zsh on macOS (it uses sysctl and sw_vers). ROOT must name a directory of
# checkouts: module roots are found there, even when the variants build from
# exported trees elsewhere.
# A VARIANT is NAME=TREEROOT[:GOEXPERIMENT]. Builds each benchmark package's
# test binary once per variant, then runs ROUNDS rounds. In each round, for
# each package, every variant runs once (-test.count 1), one right after the
# other, in an order that rotates by round. The i-th result of one variant
# therefore ran next to the i-th result of every other, so a change in the
# machine's load lands on all of them alike, and the rounds pair up.
S=${S:-$(cd "$(dirname "$0")/.." && pwd)/data}
ROOT=${ROOT:-$HOME/src/latere-ai}
out=$1; rounds=$2; benchtime=$3; shift 3
variants=("$@")
mkdir -p $out/bin
typeset -A pkgs
while read f; do
  d=$(dirname $f)
  m=$d
  while [ ! -f $ROOT/$m/go.mod ]; do m=$(dirname $m); done
  rel=${d#$m}; rel=./${rel#/}; [ "$rel" = "./" ] && rel=.
  [[ " ${pkgs[$m]} " == *" $rel "* ]] || pkgs[$m]="${pkgs[$m]} $rel"
done < ${BENCHFILES:-$S/bench-files.txt}
mods=(${(ko)pkgs})
{
  echo "start $(date -u +%FT%TZ)"
  sysctl -n machdep.cpu.brand_string hw.ncpu hw.memsize
  sw_vers 2>/dev/null | tr '\n' ' '; echo
  go version
  echo "rounds=$rounds count/round=1 benchtime=$benchtime paired"
  echo "variants: ${variants[*]}"
} >> $out/run.log
binof() { echo $out/bin/$1/$(echo $2 | tr / _)__$(echo $3 | tr /. __).test; }
for v in $variants; do
  name=${v%%=*}; rest=${v#*=}; tree=${rest%%:*}; exp=""
  [[ $rest == *:* ]] && exp=${rest#*:}
  mkdir -p $out/bin/$name $out/$name
  for m in $mods; do
    for p in ${=pkgs[$m]}; do
      b=$(binof $name $m $p)
      if ! (cd $tree/$m && GOEXPERIMENT=$exp go test -c -o $b $p >> $out/$name/build.err 2>&1); then
        echo "$(date -u +%FT%TZ) BUILDFAIL variant=$name module=$m pkg=$p" >> $out/run.log
      fi
    done
  done
done
echo "built $(date -u +%FT%TZ)" >> $out/run.log
nv=${#variants}
for r in $(seq 1 $rounds); do
  order=()
  for i in $(seq 0 $((nv - 1))); do order+=(${variants[$(( (i + r - 1) % nv + 1 ))]}); done
  echo "$(date -u +%FT%TZ) round=$r order=${(j:,:)${order[@]%%=*}} load=$(sysctl -n vm.loadavg)" >> $out/run.log
  for m in $mods; do
    for p in ${=pkgs[$m]}; do
      for v in $order; do
        name=${v%%=*}; rest=${v#*=}; tree=${rest%%:*}
        b=$(binof $name $m $p)
        [ -x $b ] || continue
        f=$out/$name/$(echo $m | tr / _).txt
        ( cd $tree/$m/$p && $b -test.run '^$' -test.bench '^BenchmarkJSON' -test.benchmem \
            -test.count 1 -test.benchtime $benchtime -test.timeout 60m >> $f 2>> $out/$name/$(echo $m | tr / _).err ) ||
          echo "$(date -u +%FT%TZ) exit=$? round=$r module=$m pkg=$p variant=$name" >> $out/run.log
      done
    done
  done
done
echo "end $(date -u +%FT%TZ)" >> $out/run.log
