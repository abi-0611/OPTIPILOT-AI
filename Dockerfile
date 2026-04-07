# Build React dashboard (required for embedded SPA at /).
FROM node:22-alpine AS uibuild
WORKDIR /ui
COPY ui/dashboard/package.json ui/dashboard/package-lock.json ./
RUN npm ci
COPY ui/dashboard/ ./
RUN npm run build

FROM golang:1.25 AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=uibuild /ui/dist ./cmd/manager/dashboard/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -tags ui -a -ldflags="-s -w" -o manager ./cmd/manager

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532
ENTRYPOINT ["/manager"]
