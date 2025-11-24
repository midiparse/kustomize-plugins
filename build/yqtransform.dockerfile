FROM golang:1.25-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY cmd cmd
COPY internal internal

RUN CGO_ENABLED=0 go build -o kustomize-plugin-yqtransform ./cmd/yqtransform

FROM alpine:latest

RUN apk --no-cache add ca-certificates

COPY --from=builder /build/kustomize-plugin-yqtransform /usr/local/bin/kustomize-plugin-yqtransform

ENTRYPOINT ["/usr/local/bin/kustomize-plugin-yqtransform"]
