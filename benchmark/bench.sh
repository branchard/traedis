#!/usr/bin/env bash
# Benchmarks: runs benchmark_test.go compiled and under Yaegi (what Traefik runs),
# on the working tree and, when a git ref is given, on that version of the plugin
# too, then compares them with benchstat.
#
#   docker compose up -d --wait cache
#   export TRAEDIS_REDIS_DSN=redis://localhost:6379/15
#   ./benchmark/bench.sh          # working tree: compiled vs Yaegi
#   ./benchmark/bench.sh main     # main vs working tree
#
# COUNT (10), BENCHTIME (500ms) and BENCH (.) are passed to -count, -benchtime and
# -bench. Raw results and report.txt are written to benchmark/results/.
set -euo pipefail

BASE="${1:-}"
COUNT="${COUNT:-10}"
BENCHTIME="${BENCHTIME:-500ms}"
BENCH="${BENCH:-.}"

MODULE="github.com/branchard/traedis"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RESULTS="$ROOT/benchmark/results"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

: "${TRAEDIS_REDIS_DSN:?must point to a Redis >= 8.0, e.g. redis://localhost:6379/15}"
if ! command -v yaegi >/dev/null; then
  echo "yaegi not found: go install github.com/traefik/yaegi/cmd/yaegi@v0.16.1" >&2
  exit 1
fi

# stage <name> [git ref]: a GOPATH holding one version of the plugin (the working
# tree without a ref) next to the benchmarks of the working tree, so that every
# version is measured by the same code. Yaegi only resolves the plugin's import
# path inside a GOPATH.
stage() {
  local dir="$TMP/$1/src/$MODULE"
  mkdir -p "$dir/benchmark"
  if [[ -n "${2:-}" ]]; then
    git -C "$ROOT" archive "$2" go.mod pkg | tar -x -C "$dir"
  else
    cp -r "$ROOT/go.mod" "$ROOT/pkg" "$dir"
  fi
  cp "$ROOT"/benchmark/*.go "$dir/benchmark"
}

# run <name>: one more sample of every benchmark, appended to the results.
run() {
  local args=(-run '^$' -bench "$BENCH" -benchmem -benchtime "$BENCHTIME" .)
  cd "$TMP/$1/src/$MODULE/benchmark"
  go test "${args[@]}" | tee -a "$RESULTS/$1.go.txt"
  GOPATH="$TMP/$1" yaegi test "${args[@]}" | tee -a "$RESULTS/$1.yaegi.txt"
}

benchstat() {
  go run golang.org/x/perf/cmd/benchstat@latest "$@"
}

versions=(head)
stage head
if [[ -n "$BASE" ]]; then
  versions=(base head)
  stage base "$BASE"
fi

mkdir -p "$RESULTS"
rm -f "$RESULTS"/*.txt

# Versions alternate within a round: a slow moment of the machine hits them all.
for round in $(seq "$COUNT"); do
  for version in "${versions[@]}"; do
    echo "# Round $round/$COUNT: $version"
    run "$version"
  done
done

cd "$RESULTS"
{
  if [[ -n "$BASE" ]]; then
    echo "# Compiled: $BASE vs working tree"
    benchstat base=base.go.txt head=head.go.txt
    echo
    echo "# Yaegi: $BASE vs working tree"
    benchstat base=base.yaegi.txt head=head.yaegi.txt
  else
    echo "# Working tree: compiled vs Yaegi"
    # Yaegi prints no "pkg:" line: without -ignore, the two would not be compared.
    benchstat -ignore pkg compiled=head.go.txt yaegi=head.yaegi.txt
  fi
} | tee report.txt
