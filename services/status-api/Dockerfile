# Multi-stage build producing a distroless, non-root image.
FROM golang:1.26-alpine AS build
WORKDIR /src

# Dependencies first so the layer is reused while only source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO off and symbols stripped: the result has to run on a distroless base.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/status-api ./cmd/status-api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/status-api /status-api
EXPOSE 8081
USER nonroot:nonroot
ENTRYPOINT ["/status-api"]
