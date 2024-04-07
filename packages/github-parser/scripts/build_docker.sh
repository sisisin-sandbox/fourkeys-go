#!/usr/bin/env bash

set -eu -o pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
readonly script_dir

function cleanup() {
  rm -rf "$script_dir/../src/vendor"
}
trap cleanup EXIT

TIMESTAMP=$(TZ=JST-9 date "+%Y%m%d-%H%M%S")
echo "$TIMESTAMP"
IMAGE_ID=sisisin/fourkeys-go-github-parser:$TIMESTAMP
echo "$IMAGE_ID"

# prepare go mod
cd "$script_dir/../src"
go mod vendor

cd "$script_dir/.."
docker build --platform linux/amd64 -t "$IMAGE_ID" .
docker login
docker push "$IMAGE_ID"

echo "Done."
echo "$IMAGE_ID"
