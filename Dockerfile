FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /pacioli ./cmd/server

FROM alpine:3.21
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /pacioli /app/pacioli
VOLUME ["/data"]
ENV DATABASE_DSN=/data/pacioli.db
EXPOSE 8080
ENTRYPOINT ["/app/pacioli"]
