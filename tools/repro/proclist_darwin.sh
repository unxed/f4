#!/usr/bin/env bash
# Throwaway: reproduce the proclist darwin SIGSEGV on a CI runner.
set -u
export CGO_ENABLED=0 GOTRACEBACK=all
uname -a; sw_vers || true; go version
fail=0
echo "== stress tests x5"
for i in 1 2 3 4 5; do
  go test ./plugins/proclist/ -run 'TestReproStress' -count=1 -v 2>&1 | tail -40 || true
  go test ./plugins/proclist/ -run 'TestReproStress' -count=1 >/dev/null 2>&1 || fail=$((fail+1))
done
echo "== full package, shuffled, 30 runs"
for i in $(seq 1 30); do
  if ! out=$(go test ./plugins/proclist/ -count=1 -shuffle=on 2>&1); then
    fail=$((fail+1)); echo "--- run $i failed"; echo "$out" | head -80
  fi
done
echo "TOTAL FAILURES: $fail"
