#!/usr/bin/make -f

VERSION := $(shell git describe)

test:
	go test --race ./...

install: test
	go install github.com/mdw-tools/dry4go/cmd/...
