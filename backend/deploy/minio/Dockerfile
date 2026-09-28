FROM golang:1.25.4-alpine
ENV GOTOOLCHAIN=local
RUN apk add --no-cache curl && go install github.com/minio/minio@RELEASE.2025-04-22T22-12-26Z
EXPOSE 9000 9001
ENTRYPOINT ["/go/bin/minio"]
CMD ["server", "/data", "--console-address", ":9001"]
