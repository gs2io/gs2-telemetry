.PHONY: build

GOPATH="$(HOME)/go"

build:
	@GOPATH=$(GOPATH) GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go mod tidy
	@GOPATH=$(GOPATH) GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/linux/amd64/gs2-telemetry main.go
	@GOPATH=$(GOPATH) GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bin/linux/arm64/gs2-telemetry main.go
	@GOPATH=$(GOPATH) GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/windows/amd64/gs2-telemetry.exe main.go
	@GOPATH=$(GOPATH) GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -o bin/windows/arm64/gs2-telemetry.exe main.go
	@GOPATH=$(GOPATH) GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o bin/macos/amd64/gs2-telemetry main.go
	@GOPATH=$(GOPATH) GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o bin/macos/arm64/gs2-telemetry main.go
