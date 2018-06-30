FROM golang:1.10
RUN go get -u github.com/golang/dep/cmd/dep
WORKDIR /go/src/copy-docker-image
ADD . .
# RUN dep ensure -h
RUN go build
RUN ./copy-docker-image --help
ENTRYPOINT ["/go/src/copy-docker-image/copy-docker-image"]