FROM golang:1.20-alpine3.18 AS build
LABEL service="go-now-here"

WORKDIR /build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o ./out/api ./app/services/api

FROM alpine:3.18 AS bin
COPY --from=build /build/out/api /app/api

RUN apk update                                && \
    apk add --no-cache                           \
      curl                                       \
      bash

RUN addgroup -g 1000 -S api                   && \
    adduser -u 1000 -h /app -G api -S api

COPY ui /app/ui

WORKDIR /app

USER api

ENTRYPOINT ["/app/api"]