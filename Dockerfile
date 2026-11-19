FROM golang:1.25.2-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /harbor .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /harbor /harbor
ENTRYPOINT ["/harbor"]
