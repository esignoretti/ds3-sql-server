FROM golang:1.22-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /ds3sql-server ./cmd/ds3sql-server

FROM gcr.io/distroless/base-debian12

COPY --from=builder /ds3sql-server /ds3sql-server

EXPOSE 8080
USER nobody

ENTRYPOINT ["/ds3sql-server"]
