FROM golang:1.8

WORKDIR /go/src/app
COPY main.go .
COPY vendor /go/src/
#COPY src/ /go/src/
RUN ls -l /go
#RUN go-wrapper download   # "go get -d -v ./..."
RUN go install -v ./...    # "go install -v ./..."

ENTRYPOINT ["app"]
