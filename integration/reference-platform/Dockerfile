# syntax=docker/dockerfile:1.20
FROM docker.io/library/golang:1.26.5-alpine3.24@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS build

WORKDIR /src

COPY . .

ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH="${TARGETARCH}" go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/reference-platform \
    ./pkg/service/integration/reference-platform/cmd/reference-platform

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 --chmod=0555 /out/reference-platform /reference-platform

USER 65532:65532

ENV TMPDIR=/tmp

EXPOSE 8080 8081

ENTRYPOINT ["/reference-platform"]
CMD ["serve"]
