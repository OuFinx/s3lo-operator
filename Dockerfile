FROM gcr.io/distroless/static:nonroot
COPY s3lo-proxy /usr/local/bin/s3lo-proxy
ENTRYPOINT ["s3lo-proxy"]
