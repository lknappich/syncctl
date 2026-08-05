.PHONY: all build test vet lint fmt fmt-check coverage vuln docker release-snapshot clean install

all: build

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run --timeout 5m

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt issues:"; echo "$$out"; exit 1; fi

coverage:
	go test -race -coverprofile=coverage.txt ./...

vuln:
	govulncheck ./...

docker:
	docker build -t syncctl:dev .

release-snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -f coverage.txt

install:
	go install ./cmd/syncctl