FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/rssh-logger ./cmd/rssh-logger \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/edge-logger ./cmd/edge-logger \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/nginx-edge-forwarder ./cmd/nginx-edge-forwarder \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/rssh-error-forwarder ./cmd/rssh-error-forwarder \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/rssh-session-reconciler ./cmd/rssh-session-reconciler

FROM alpine:3.22

RUN addgroup -S app && adduser -S -G app app \
 && mkdir -p /data \
 && chown -R app:app /data

COPY --from=build /out/rssh-logger /usr/local/bin/rssh-logger
COPY --from=build /out/edge-logger /usr/local/bin/edge-logger
COPY --from=build /out/nginx-edge-forwarder /usr/local/bin/nginx-edge-forwarder
COPY --from=build /out/rssh-error-forwarder /usr/local/bin/rssh-error-forwarder
COPY --from=build /out/rssh-session-reconciler /usr/local/bin/rssh-session-reconciler

USER app
VOLUME ["/data"]
EXPOSE 8080
CMD ["/usr/local/bin/rssh-logger"]
