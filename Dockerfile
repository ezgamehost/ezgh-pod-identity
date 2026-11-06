# Build context: the repository root. The pod-identity-agent add-on's image: one static binary on
# distroless. It runs as root with every capability dropped but NET_BIND_SERVICE (port 80 on the
# host network); Kubernetes gives added capabilities only to root containers.
FROM mirror.gcr.io/library/golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ezgh-pod-identity-agent ./cmd/ezgh-pod-identity-agent

FROM gcr.io/distroless/static-debian12 AS runtime
COPY --from=build /out/ezgh-pod-identity-agent /ezgh-pod-identity-agent
ENTRYPOINT ["/ezgh-pod-identity-agent"]
