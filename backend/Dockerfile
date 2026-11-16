# syntax=docker/dockerfile:1
# Keep the Go version in step with the `go` line in go.mod.
FROM golang:1.25 AS build
WORKDIR /src
# Dependencies first, so this layer stays cached until go.mod/go.sum change.
# go.sum is copied and `go mod download` is used instead of `go mod tidy`, so the
# image builds exactly the dependency versions that were committed and tested.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/api

# distroless/static includes CA certificates (needed for TLS to Neon/Lakebase,
# Gemini and mcdvoice.com) and runs as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
ENTRYPOINT ["/server"]
