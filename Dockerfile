FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod *.go ./
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /lan-copy .

FROM scratch
COPY --from=build /lan-copy /lan-copy
ENV LAN_COPY_DISABLE_UPDATE=1
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/lan-copy", "-dir", "/data"]
