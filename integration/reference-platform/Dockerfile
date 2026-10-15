FROM docker.io/library/golang:1.27.2-alpine3.24@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS build

WORKDIR /src

COPY integration/reference-platform/ ./integration/reference-platform/

WORKDIR /src/integration/reference-platform

ENV GOENV=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly \
    GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org \
    GOPRIVATE="" GONOPROXY="" GONOSUMDB=""

ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=linux GOARCH="${TARGETARCH}" go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/reference-platform \
    ./cmd/reference-platform

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 --chmod=0555 /out/reference-platform /reference-platform

USER 65532:65532

ENV TMPDIR=/tmp

EXPOSE 8080 8081

ENTRYPOINT ["/reference-platform"]
CMD ["serve"]
