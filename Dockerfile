ARG GO_VERSION=1.26.4

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/hobbydns main.go

FROM alpine:3.22
RUN addgroup -S hobbydns && adduser -S -G hobbydns hobbydns

WORKDIR /data
COPY --from=build /out/hobbydns /usr/local/bin/hobbydns

EXPOSE 53/tcp 53/udp
USER hobbydns
ENTRYPOINT ["hobbydns"]
