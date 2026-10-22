FROM docker.io/library/golang:1.27.3-alpine3.24@sha256:d530cf60723abf1ffae0cd16bb546b379ec0550d2f3815e5b16ed69dbc00fba4 AS build

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
