FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gbfs_exporter .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gbfs_exporter /gbfs_exporter
EXPOSE 9718
USER nonroot:nonroot
ENTRYPOINT ["/gbfs_exporter"]
CMD ["-config", "/etc/gbfs_exporter/config.yml"]
