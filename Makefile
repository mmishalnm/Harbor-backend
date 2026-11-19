.PHONY: build run fmt check integration docs
build:
	go build -trimpath -o harbor .
run:
	go run .
fmt:
	gofmt -w main.go cmd internal
check:
	@test -z "$$(gofmt -l main.go cmd internal)"
	go vet ./...
	go test ./...
integration:
	@test -n "$$HARBOR_TEST_MONGO_URI" || (echo "Set HARBOR_TEST_MONGO_URI to a disposable replica set"; exit 1)
	go test ./... -count=1
docs:
	python3 scripts/generate_openapi.py
