FROM alpine:3.11
RUN apk add --update ca-certificates tzdata && rm -rf /var/cache/apk/*
ADD build/gke-node-optimizer /bin
ENTRYPOINT ["/bin/gke-node-optimizer"]
