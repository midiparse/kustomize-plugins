FROM golang:1.26-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY cmd cmd
COPY internal internal

RUN CGO_ENABLED=0 go build -o kustomize-plugin-vendorkustomization ./cmd/vendorkustomization

FROM alpine:latest

RUN apk --no-cache add ca-certificates

COPY --from=builder /build/kustomize-plugin-vendorkustomization /usr/local/bin/kustomize-plugin-vendorkustomization

ENTRYPOINT ["/usr/local/bin/kustomize-plugin-vendorkustomization"]
