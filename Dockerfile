FROM golang:1.17 as builder
WORKDIR /tmp/builder

COPY go.mod ./go.mod
COPY go.sum ./go.sum
RUN  go mod download

COPY . ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$(go env GOARCH) go build -ldflags="-s -w" -o broker .


FROM alpine:3.14

RUN apk update && \
    apk add --no-cache ca-certificates && \
    update-ca-certificates && \
    rm -rf /var/cache/apk/*

COPY --from=builder /tmp/builder/broker /usr/bin/broker

ENTRYPOINT [ "/usr/bin/broker" ]
