FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /processor ./cmd/processor

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /data
COPY --from=build /processor /processor
EXPOSE 8080
ENTRYPOINT ["/processor"]
