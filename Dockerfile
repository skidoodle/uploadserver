FROM golang:1.26.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY main.go ./
COPY internal/ ./internal/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOFLAGS=-trimpath \
    go build -ldflags="-s -w" -o /uploadserver .
RUN mkdir -p /skel/data /skel/state

FROM scratch
USER 1000:1000
COPY --chown=1000:1000 --from=build /skel/data /data
COPY --chown=1000:1000 --from=build /skel/state /state
COPY --from=build /uploadserver /uploadserver
EXPOSE 8080
ENV LISTEN_ADDR=":8080" UPLOAD_DIR="/data" TOKEN_STORE="/state/tokens.db"
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/uploadserver", "healthcheck"]
ENTRYPOINT ["/uploadserver"]
CMD ["run"]
