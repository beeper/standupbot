FROM docker.io/golang:1-alpine3.16 AS builder

RUN apk add --no-cache git ca-certificates build-base su-exec olm-dev

COPY . /build
WORKDIR /build
RUN go build -o /usr/bin/standupbot

FROM docker.io/alpine:3.16

ENV UID=1337 \
    GID=1337

RUN apk add --no-cache su-exec ca-certificates olm bash tzdata

COPY --from=builder /usr/bin/standupbot /usr/bin/standupbot
COPY --from=builder /build/config.sample.json /opt/standupbot/config.sample.json
COPY --from=builder /build/docker-run.sh /docker-run.sh
VOLUME /data

CMD ["/docker-run.sh"]
