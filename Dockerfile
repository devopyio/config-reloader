FROM golang:1-alpine AS build
LABEL Maintainer="info@devopy.io" Description="Fully automated Zabbix and Prometheus Alertmanager integration"
RUN apk update && apk add make git gcc musl-dev

ADD . /go/src/github.com/devopyio/zabbix-alertmanager

WORKDIR /go/src/github.com/devopyio/zabbix-alertmanager

ENV GO111MODULE on
RUN make build
RUN mv config-reloader /app

FROM alpine:latest

RUN apk add --no-cache ca-certificates && mkdir /app
RUN adduser app -u 1001 -g 1001 -s /bin/false -D app

COPY --from=build /app /usr/bin
RUN chown -R app /usr/bin/config-reloader

USER app
ENTRYPOINT ["/usr/bin/config-reloader"]
