#!/usr/bin/env bash
# Print the `go test -run` regex selecting shard $1 of $2 for the given packages.
#
#   select-tests.sh <shard> <shard-count> <package>...
#
# Every top-level test is enumerated from the source tree at run time, so a
# newly added test always lands in exactly one shard: the shards partition the
# suite, they never filter it. test-weights.tsv only decides *which* shard a
# test goes to; a missing entry costs balance, never coverage.
#
# Balancing is per package: each package's tests are bin-packed (longest
# processing time first) across all shards, so every shard gets a slice of
# every package and keeps the cross-package parallelism `go test` gives us.
set -euo pipefail

if [ "$#" -lt 3 ]; then
	echo "usage: $0 <shard-index-1-based> <shard-count> <package>..." >&2
	exit 2
fi

shard=$1
shards=$2
shift 2

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
weights=${script_dir}/test-weights.tsv

if [ "$shard" -lt 1 ] || [ "$shard" -gt "$shards" ]; then
	echo "shard $shard out of range 1..$shards" >&2
	exit 2
fi

dirs=()
for pkg in "$@"; do
	pkg=${pkg#./}
	case "$pkg" in
	*/...)
		while IFS= read -r dir; do
			dirs+=("${dir#./}")
		done < <(find "${pkg%/...}" -type d | sort)
		;;
	*) dirs+=("$pkg") ;;
	esac
done

# Median of the known weights, used for tests that predate the last refresh of
# test-weights.tsv (or were added since).
default_weight=$(awk -F'\t' '!/^#/ && NF == 3 { print $3 }' "$weights" | sort -n |
	awk '{ v[NR] = $1 } END { if (NR) print v[int((NR + 1) / 2)]; else print 30 }')

names=$(
	for dir in "${dirs[@]}"; do
		compgen -G "${dir}/*_test.go" >/dev/null || continue
		sed -n 's/^func \(Test[A-Za-z0-9_]*\)(.*/\1/p' "${dir}"/*_test.go |
			sort -u | sed "s|^|${dir}\t|"
	done
)

if [ -z "$names" ]; then
	echo "no tests found in: $*" >&2
	exit 1
fi


# Attach a weight to every test, then hand the list to the packer sorted by
# package, weight descending, name — the input order LPT bin-packing needs.
printf '%s\n' "$names" |
	awk -F'\t' -v weights="$weights" -v def="$default_weight" '
		BEGIN { while ((getline line < weights) > 0) { n = split(line, f, "\t"); if (line !~ /^#/ && n == 3) w[f[1] SUBSEP f[2]] = f[3] } }
		{ key = $1 SUBSEP $2; print $1 "\t" ((key in w) ? w[key] : def) "\t" $2 }
	' |
	sort -t"$(printf '\t')" -k1,1 -k2,2nr -k3,3 |
	awk -F'\t' -v shard="$shard" -v shards="$shards" '
		function reset(i) { for (i = 1; i <= shards; i++) load[i] = 0 }
		BEGIN { reset(); pkg = "" }
		{
			if ($1 != pkg) { pkg = $1; reset() }
			best = 1
			for (i = 2; i <= shards; i++) if (load[i] < load[best]) best = i
			load[best] += $2
			if (best == shard) out = out (out ? "|" : "") $3
		}
		END {
			if (out == "") { print "shard " shard " of " shards " matched no tests" > "/dev/stderr"; exit 1 }
			print "^(" out ")$"
		}
	'
