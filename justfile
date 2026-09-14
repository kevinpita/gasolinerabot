set dotenv-load

_default:
    @just --list

run:
    go run ./cmd/gasolinerabot

build:
    CGO_ENABLED=0 go build -trimpath -o bin/gasolinerabot ./cmd/gasolinerabot

fmt:
    gofmt -w cmd internal

test:
    go test -race -shuffle=on -count=1 -timeout=5m ./...
    python3 -m unittest discover -s scripts -p 'test_*.py'

check: test
    test -z "$(gofmt -l cmd internal)"
    go mod tidy -diff
    go vet ./...
    go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
    bash scripts/check-infra.sh
    terraform -chdir=infra/openbao fmt -check

# TEST_DATABASE_URL must point to a disposable database. Tests truncate its tables.
check-db:
    test -n "$TEST_DATABASE_URL"
    just check

docker-build:
    ${CONTAINER_ENGINE:-docker} build --platform linux/amd64 -t gasolinerabot:dev .

docker-smoke:
    bash scripts/smoke-image.sh gasolinerabot:dev
