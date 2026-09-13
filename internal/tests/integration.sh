#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
command -v curl >/dev/null || { echo "integration tests require curl" >&2; exit 1; }
container="wave-test-${RANDOM}-$$"
s3_container="${container}-s3"
cleanup() { docker rm -fv "$container" "$s3_container" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker run --name "$container" -e POSTGRES_USER=wave -e POSTGRES_PASSWORD=wave-test -e POSTGRES_DB=wave_test -p 127.0.0.1::5432 -d postgres:16-alpine >/dev/null
ready=false
for _ in $(seq 1 60); do
  if docker exec "$container" pg_isready -U wave >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.5
done
if [[ "$ready" != true ]]; then echo "test PostgreSQL did not become ready" >&2; exit 1; fi
port=$(docker port "$container" 5432/tcp | head -1 | cut -d: -f2)
export WAVE_TEST_DATABASE_URL="postgres://wave:wave-test@127.0.0.1:${port}/wave_test?sslmode=disable"

# Exercise the same SeaweedFS release and S3 options as the default deployment.
docker run --name "$s3_container" -e AWS_ACCESS_KEY_ID=wave-test -e AWS_SECRET_ACCESS_KEY=wave-test-password -e S3_BUCKET=wave -p 127.0.0.1::8333 -d chrislusf/seaweedfs:4.46 mini -dir=/data -ip=127.0.0.1 -master.telemetry=false -admin.ui=false -webdav=false -s3.port.iceberg=0 -s3.port.lance=0 -s3.autoCreateBucket=false >/dev/null
s3_port=$(docker port "$s3_container" 8333/tcp | head -1 | cut -d: -f2)
export WAVE_TEST_S3_ENDPOINT="http://127.0.0.1:${s3_port}"
ready=false
for _ in $(seq 1 120); do
  if curl -fsS --head --max-time 1 --aws-sigv4 aws:amz:us-east-1:s3 --user wave-test:wave-test-password "$WAVE_TEST_S3_ENDPOINT/wave" >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.5
done
if [[ "$ready" != true ]]; then echo "test S3 server did not become ready" >&2; exit 1; fi

WAVE_DATABASE_URL="$WAVE_TEST_DATABASE_URL" go run ./cmd/wave init-db
# Tests mutate shared schema callbacks and run real competing workers; packages
# execute sequentially, concurrency inside each test remains real.
go test -race -p 1 ./... -count=1
