FROM docker.io/golang:alpine as builder

RUN apk add --no-cache olm-dev gcc musl-dev libstdc++-dev

COPY . /app
WORKDIR /app

RUN go build

FROM docker.io/alpine

RUN apk add --no-cache olm libstdc++ tzdata

COPY --from=builder /app/standupbot /usr/local/bin/standupbot
