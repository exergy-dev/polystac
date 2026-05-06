# syntax=docker/dockerfile:1.7
#
# PolyStac container — pure-Go static binary on distroless. ~25–30 MB.
# Multi-arch: amd64/arm64 via buildx.
#
# Operators wanting the DuckDB backend (CGO) must build a separate image
# from the duckdb-tagged source — out of scope for the default image.

FROM --platform=$BUILDPLATFORM golang:1.25 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/polystac ./cmd/polystac

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/polystac /polystac
EXPOSE 8000
USER nonroot:nonroot
ENTRYPOINT ["/polystac"]
CMD ["serve"]
