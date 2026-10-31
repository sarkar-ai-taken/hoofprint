FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X github.com/sarkar-ai-taken/hoofprint/internal/version.Version=${VERSION}" -o /out/hoofprint-agent ./cmd/agent

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/hoofprint-agent /hoofprint-agent
USER nonroot
ENTRYPOINT ["/hoofprint-agent"]
