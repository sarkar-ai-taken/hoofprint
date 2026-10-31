FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X github.com/sarkar-ai-taken/hoofprint/internal/version.Version=${VERSION}" -o /out/hoofprint-api ./cmd/api \
 && CGO_ENABLED=0 go build -ldflags "-s -w -X github.com/sarkar-ai-taken/hoofprint/internal/version.Version=${VERSION}" -o /out/hoofprint ./cmd/cli

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/hoofprint-api /hoofprint-api
COPY --from=build /out/hoofprint /hoofprint
EXPOSE 8080 9090
USER nonroot
ENTRYPOINT ["/hoofprint-api"]
