.PHONY: build test vet check e2e fmt clean

build:
	go build -o burning .

test:
	go test ./...

vet:
	go vet ./...

check: test vet

e2e:
	go test -count=1 ./...
	BURNING_E2E=1 go test -count=1 -run '^TestLiveProviders$$' .

fmt:
	go fmt ./...

clean:
	rm -f burning
