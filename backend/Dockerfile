FROM golang:1.25.4-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY . .
ARG SERVICE
WORKDIR /src/services/${SERVICE}
ENV GOTOOLCHAIN=local
RUN go build -o /out/service ./cmd

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
ARG SERVICE
COPY --from=build /out/service /service
COPY services/${SERVICE}/config.docker.yaml /config.yaml
COPY deploy/keys /keys
EXPOSE 50051
ENTRYPOINT ["/service", "-config", "/config.yaml"]
