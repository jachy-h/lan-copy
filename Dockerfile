FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /lan-copy .

FROM scratch
COPY --from=build /lan-copy /lan-copy
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/lan-copy", "-dir", "/data"]
