FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY shared shared
COPY server server
RUN cd server && GOWORK=off CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /prata ./cmd/server

FROM gcr.io/distroless/static:nonroot
COPY --from=build /prata /prata
ENV PATH=/:$PATH
VOLUME /data
EXPOSE 8080
HEALTHCHECK CMD ["/prata", "healthcheck"]
ENTRYPOINT ["/prata"]
