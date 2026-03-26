FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /out/proxy ./cmd/proxy
RUN go build -o /out/api ./cmd/api
RUN go build -o /out/optimizer ./cmd/optimizer

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /out/proxy /usr/local/bin/proxy
COPY --from=builder /out/api /usr/local/bin/api
COPY --from=builder /out/optimizer /usr/local/bin/optimizer
ENV PG_LOG_LEVEL=debug
EXPOSE 8080 3001
ENTRYPOINT ["/usr/local/bin/proxy"]
