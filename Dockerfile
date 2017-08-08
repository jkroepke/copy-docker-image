FROM golang:1.8

WORKDIR /go/src/app
COPY main.go .
COPY vendor /go/src/

RUN go install -v ./...

ENTRYPOINT ["app"]
