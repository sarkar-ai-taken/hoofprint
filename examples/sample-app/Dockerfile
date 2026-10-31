# Build from the repo root: docker build -f examples/sample-app/Dockerfile -t hoofprint/sample-app:dev .
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG APP_VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/sample-app ./examples/sample-app

FROM gcr.io/distroless/static-debian12:nonroot
ARG APP_VERSION=dev
ENV APP_VERSION=${APP_VERSION}
COPY --from=build /out/sample-app /sample-app
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/sample-app"]
