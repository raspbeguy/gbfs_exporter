FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

# Docker sets TARGETOS and TARGETARCH from the target platform. The build stage
# runs on the native platform and cross-compiles, so no emulation is necessary.
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/gbfs_exporter .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gbfs_exporter /gbfs_exporter
EXPOSE 9718
USER nonroot:nonroot
ENTRYPOINT ["/gbfs_exporter"]
CMD ["-config", "/etc/gbfs_exporter/config.yml"]
