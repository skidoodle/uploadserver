# syntax=docker/dockerfile:1
# check=skip=SecretsUsedInArgOrEnv
FROM golang:1.26.6-alpine AS build
ENV GOTOOLCHAIN=local
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

COPY main.go ./
COPY tools/ ./tools/
COPY internal/ ./internal/

RUN go generate ./...

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOFLAGS=-trimpath \
    go build -ldflags="-s -w -X uploadserver/internal.Version=${VERSION} -X uploadserver/internal.Commit=${COMMIT} -X uploadserver/internal.Date=${DATE}" -o /uploadserver .

RUN mkdir -p /skel/data /skel/state && \
    for cmd in run healthcheck list info add rm disable enable limit global scan migrate prune export import dump reset version help; do \
        ln -s /uploadserver /skel/$cmd; \
    done

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/skidoodle/uploadserver"
USER 1000:1000
COPY --chown=1000:1000 --from=build /skel/data /data
COPY --chown=1000:1000 --from=build /skel/state /state
COPY --from=build /uploadserver /uploadserver
COPY --from=build /skel/ /
VOLUME ["/data", "/state"]
EXPOSE 8080
ENV LISTEN_ADDR=":8080" UPLOAD_DIR="/data" TOKEN_STORE="/state/tokens.db" PATH="/"
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/uploadserver", "healthcheck"]
ENTRYPOINT ["/uploadserver"]
CMD ["run"]
