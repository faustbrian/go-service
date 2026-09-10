# syntax=docker/dockerfile:1.20
FROM docker.io/library/golang:1.27.0-alpine3.24@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS build

WORKDIR /src

COPY . .

ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH="${TARGETARCH}" go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/reference-platform \
    ./integration/reference-platform/cmd/reference-platform

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 --chmod=0555 /out/reference-platform /reference-platform

USER 65532:65532

ENV TMPDIR=/tmp

EXPOSE 8080 8081

ENTRYPOINT ["/reference-platform"]
CMD ["serve"]
