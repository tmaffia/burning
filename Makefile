.PHONY: build test vet fmtcheck check e2e fmt clean

build:
	go build -o burning .

test:
	go test ./...

vet:
	go vet ./...

fmtcheck:
	@files="$$(gofmt -l .)"; test -z "$$files" || { printf '%s\n' "$$files"; exit 1; }

check: fmtcheck vet test

e2e:
	go test -count=1 ./...
	BURNING_E2E=1 go test -count=1 -run '^TestLiveProviders$$' .

fmt:
	go fmt ./...

clean:
	rm -f burning
