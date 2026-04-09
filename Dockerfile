FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY s3lo-proxy /usr/local/bin/s3lo-proxy
ENTRYPOINT ["s3lo-proxy"]
