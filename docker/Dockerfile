FROM golang:1.20-alpine3.18 AS build
LABEL service="go-now-here"

WORKDIR /build
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o ./out/api ./app/services/chat-api

FROM alpine:3.18 AS bin
COPY --from=build /build/out/api /app/api

EXPOSE 8000

WORKDIR /app

RUN apk update                                                         && \
    apk add --no-cache                                                    \
      curl                                                                \
      bash

RUN adduser --disabled-password -s /bin/bash api                       && \
    chown api /app

COPY ui ui

USER api

ENTRYPOINT ["/app/api"]