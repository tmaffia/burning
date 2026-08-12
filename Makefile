.PHONY: build test vet check fmt clean

build:
	go build -o burning .

test:
	go test ./...

vet:
	go vet ./...

check: test vet

fmt:
	go fmt ./...

clean:
	rm -f burning
