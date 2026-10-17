# Finesse SSE server, no Ruby or Rails. Build: docker build --build-arg VERSION=1.2.3 .
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY ext/finesse/go.mod ext/finesse/go.sum ./
RUN go mod download
COPY ext/finesse/ ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /finesse .

FROM scratch
# CA bundle so --db-url can verify PostgreSQL TLS.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /finesse /finesse
ENV BINDING=0.0.0.0
EXPOSE 4000
ENTRYPOINT ["/finesse"]
