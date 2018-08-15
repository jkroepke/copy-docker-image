#!/usr/bin/env bash
docker run --rm -ti -v$PWD:/go/src/copy-docker-image -w /go/src/copy-docker-image golang \
  bash -c "go get -u github.com/golang/dep/cmd/dep && dep ensure -update -v"

docker run --rm -ti -v$PWD:/go/src/copy-docker-image -w /go/src/copy-docker-image golang \
  bash -c "go version && go build && ./copy-docker-image --help"