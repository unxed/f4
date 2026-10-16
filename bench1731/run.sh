#!/usr/bin/env bash
# Temporary measurement stand for unxed/f4#1731 (tmp branch only).
# Builds a mixed ~500 MB tree, packs it with 7-Zip in several ways and compares
# 7-Zip with the f4 engine (unxed/archives + unxed/sevenzip + unxed/xz).
set +e
export LC_ALL=C.UTF-8
W=/tmp/b1731
mkdir -p "$W/data" "$W/prof"
R=$(pwd)
tms() { date +%s%N; }

echo "##### machine"
nproc; lscpu | grep -E 'Model name|^CPU\(s\)|^Thread|MHz|L2 cache|L3 cache'
free -m | head -2; uname -r; go version

echo "##### 7-Zip"
sudo apt-get update -qq >/dev/null 2>&1
sudo apt-get install -y -qq 7zip >/dev/null 2>&1 || sudo apt-get install -y -qq p7zip-full >/dev/null 2>&1
Z=$(command -v 7zz || command -v 7z)
echo "7z binary: $Z"
"$Z" 2>&1 | sed -n 1,2p

echo "##### build f4 test binary"
s=$(tms)
go test -c -o "$W/archive.test" ./plugins/archive 2>&1 | tail -20
echo "build ms: $(( ($(tms) - s) / 1000000 ))"
ls -la "$W/archive.test" || { echo "BUILD FAILED"; exit 1; }
T="$W/archive.test"

echo "##### generate data"
python3 -I "$R/bench1731/gen.py" "$W/data"
du -sm "$W/data"

mk() { # name, 7z args...
  local name=$1; shift
  rm -f "$W/$name.7z"
  local s; s=$(tms)
  (cd "$W/data" && "$Z" a -t7z -bso0 -bsp0 "$@" "$W/$name.7z" ./* )
  echo "CREATE $name [$*]: $(( ($(tms) - s) / 1000000 )) ms, size $(stat -c %s "$W/$name.7z") bytes"
  "$Z" l -slt "$W/$name.7z" > "$W/$name.lst" 2>&1
  echo "  methods per block (count of files):"
  grep -E '^(Method|Block) =' "$W/$name.lst" | paste - - | sort | uniq -c | sed 's/^/    /'
  grep -E '^(Physical Size|Headers Size|Solid|Blocks)' "$W/$name.lst" | head -6 | sed 's/^/  /'
  "$Z" l "$W/$name.7z" | tail -2 | sed 's/^/  /'
}
echo "##### create archives"
mk A -mx5 -m0=lzma2 -mf=BCJ -mmt=on
mk B -mx5 -m0=lzma2 -mf=BCJ -mmt=on -ms=100m
mk C -mx5 -m0=lzma2 -mf=BCJ -mmt=1
mk D -mx5 -m0=lzma2 -mf=off -mmt=on

cpus() { seq -s, 0 $(( $1 - 1 )); }
tm() { # label, cmd...
  local label=$1; shift
  TIMEFORMAT="TIME $label: %R s wall, %U s user, %S s sys"
  time "$@" >/dev/null 2>"$W/err.txt"
  local rc=$?
  [ $rc -ne 0 ] && { echo "  rc=$rc"; tail -3 "$W/err.txt"; }
}
gt() { # label, ncores, archive, mode, extra go-test args...
  local label=$1 n=$2 a=$3 m=$4; shift 4
  echo "--- GO $label ($a, mode=$m, cores=$n)"
  BENCH1731_ARCHIVE="$W/$a.7z" BENCH1731_MODE=$m taskset -c "$(cpus "$n")" "$T" \
    -test.run '^TestBench1731$' -test.v -test.count=1 -test.timeout=20m "$@" 2>&1 \
    | grep -E 'BENCH1731|^(--- )?FAIL|panic|fatal|Error' | grep -v 'BENCH1731 start'
}
prof() { # name
  echo "=== pprof flat: $1"
  go tool pprof -top -nodecount=40 "$T" "$W/prof/$1.cpu" 2>/dev/null | sed -n 1,50p
  echo "=== pprof cum: $1"
  go tool pprof -top -cum -nodecount=30 "$T" "$W/prof/$1.cpu" 2>/dev/null | sed -n 4,40p
}

echo "##### 7-Zip timings"
for n in 4 2 1; do
  for run in 1 2; do
    tm "7z t A cores=$n run=$run" taskset -c "$(cpus $n)" "$Z" t -mmt=$n "$W/A.7z"
  done
done
for a in B C D; do
  tm "7z t $a cores=4" taskset -c 0,1,2,3 "$Z" t -mmt=4 "$W/$a.7z"
  tm "7z t $a cores=1" taskset -c 0 "$Z" t -mmt=1 "$W/$a.7z"
done

echo "##### f4 engine: last member of each block (A), cores=4"
gt "folders A" 4 A folders -test.cpuprofile="$W/prof/A_folders_4.cpu" > "$W/folders.txt"
cat "$W/folders.txt"

echo "##### 7-Zip: extract the same last members (A)"
grep 'BENCH1731 LAST' "$W/folders.txt" | while read -r _ _ stream _ _ name; do
  nm=${name#name=}
  # name may contain only safe characters (see gen.py)
  for n in 4 1; do
    tm "7z e -so A ${stream} ($nm) cores=$n" taskset -c "$(cpus $n)" "$Z" e -so -mmt=$n "$W/A.7z" "$nm"
  done
done

echo "##### f4 engine: last member of each block (A), cores=1"
gt "folders A" 1 A folders

echo "##### f4 engine: whole-archive test (production path testArchiveOnce)"
for run in 1 2; do gt "test A run=$run" 4 A test; done
gt "test A" 4 A test -test.cpuprofile="$W/prof/A_test_4.cpu" -test.memprofile="$W/prof/A_test_4.mem"
gt "test A" 2 A test
gt "test A" 1 A test -test.cpuprofile="$W/prof/A_test_1.cpu"
gt "seq A" 4 A seq -test.cpuprofile="$W/prof/A_seq_4.cpu"
gt "seq A" 1 A seq
for a in B C D; do
  gt "test $a" 4 $a test
  gt "test $a" 1 $a test
done
gt "folders D" 4 D folders
gt "folders C" 4 C folders

echo "##### pprof"
prof A_test_4
prof A_test_1
prof A_seq_4
prof A_folders_4
echo "=== alloc_space A_test_4"
go tool pprof -sample_index=alloc_space -top -nodecount=15 "$T" "$W/prof/A_test_4.mem" 2>/dev/null | sed -n 1,25p
echo "##### done"
