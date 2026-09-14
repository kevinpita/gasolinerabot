FROM docker.io/library/golang:1.26.7-bookworm@sha256:659cc38c1a394eeb4dd7e31fff6df128bd33444dcc7afd70e3bed5225749dbc0 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" -o /gasolinerabot ./cmd/gasolinerabot

FROM gcr.io/distroless/static-debian13:nonroot@sha256:2293b36c7c9082bf4115aab724b4d2cddec82c8eba39bf27ac0517e159acf150
COPY --from=build /gasolinerabot /gasolinerabot
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/gasolinerabot"]
