FROM golang:1.17 AS builder

RUN mkdir -p /src
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

RUN go get github.com/rqlite/go-sqlite3

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o tqlite -v ./cmd/tqlite
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -ldflags "-extldflags -static" -o tqlited -v ./cmd/tqlited

FROM alpine:3.14

COPY --from=builder /src/tqlite /src/tqlited /usr/local/bin/
RUN apk add --no-cache bash

ENV TQLITE_VERSION=1.0.0
RUN mkdir -p /tqlite/file
VOLUME /tqlite/file

EXPOSE 4001 4002
COPY ./script/docker-entrypoint.sh /bin/docker-entrypoint.sh

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["tqlited", "-http-addr", "0.0.0.0:4001", "-raft-addr", "0.0.0.0:4002", "/tqlite/file/data"]