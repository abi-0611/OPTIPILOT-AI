FROM golang:1.25 AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -a -ldflags="-s -w" -o manager ./cmd/manager

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532
ENTRYPOINT ["/manager"]
