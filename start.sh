#!/usr/bin/env bash
#
# Runs the enron-mail-processor Docker image.
#
# Usage: ./start.sh [--in-memory] <dataset>
#
# The dataset is mounted read-only into the container. The database and
# results.txt are written to the current directory.

set -euo pipefail

readonly IMAGE="enron-mail-processor"
readonly PORT=8080

usage() {
    cat <<EOF
Usage: $0 [--in-memory] <dataset>

Arguments:
  <dataset>    Path to the Enron mail TAR archive.

Options:
  --in-memory  Use an in-memory SQLite database instead of a file.
  -h, --help   Show this help.
EOF
}

dataset=""
in_memory=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --in-memory)
            in_memory=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        -*)
            echo "error: unknown option: $1" >&2
            usage >&2
            exit 1
            ;;
        *)
            if [[ -n "$dataset" ]]; then
                echo "error: unexpected argument: $1" >&2
                usage >&2
                exit 1
            fi
            dataset="$1"
            shift
            ;;
    esac
done

if [[ -z "$dataset" ]]; then
    echo "error: dataset path is required" >&2
    usage >&2
    exit 1
fi
if [[ ! -f "$dataset" ]]; then
    echo "error: dataset not found: $dataset" >&2
    exit 1
fi
dataset="$(realpath "$dataset")"

if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
    echo "error: Docker image '$IMAGE' not found. Build it first:" >&2
    echo "  docker build -t $IMAGE ." >&2
    exit 1
fi

args=(--path /archive.tar.gz)
if [[ "$in_memory" == true ]]; then
    args+=(--db memory)
fi

# Running as the current user lets the container write to the current
# directory without changing its permissions.
exec docker run --rm \
    -p "$PORT:8080" \
    --user "$(id -u):$(id -g)" \
    -v "$dataset:/archive.tar.gz:ro" \
    -v "$PWD:/data" \
    "$IMAGE" "${args[@]}"
