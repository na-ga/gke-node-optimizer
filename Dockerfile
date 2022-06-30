# Build in a stock Go builder container
FROM golang:1.18-alpine as builder
RUN apk add --no-cache make gcc musl-dev linux-headers git
ADD . /gke-node-optimizer
RUN cd /gke-node-optimizer && make build

# Pull into a second stage deploy alpine container
FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata && rm -rf /var/cache/apk/*
COPY --from=builder /gke-node-optimizer/build/gke-node-optimizer /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/gke-node-optimizer"]
