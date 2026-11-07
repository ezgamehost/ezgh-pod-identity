# ezgh-pod-identity

Workload credentials for [EZGH Kubernetes](https://docs.ezghcloud.com). A pod whose service account
has a pod identity association receives short-lived EZGH API keys for the associated Bot, with no
long-lived secret stored in the cluster.

EZGH Kubernetes installs and upgrades this component for you. This repository holds its source
code; you don't need to deploy it yourself.

## How it works

1. You create a pod identity association between a Kubernetes service account and an EZGH Bot.
2. When a pod using that service account is created, the webhook adds a projected service account
   token and two environment variables: `EZGH_CONTAINER_CREDENTIALS_FULL_URI` and
   `EZGH_CONTAINER_AUTHORIZATION_TOKEN_FILE`.
3. The EZGH SDKs and CLI read those variables and request credentials from the node's agent.
4. The agent exchanges the pod's token for a short-lived Bot API key on every request. Nothing is
   cached on the node, and deleting the association stops new credentials immediately.

## Components

| Binary | Runs on | Purpose |
| --- | --- | --- |
| `ezgh-pod-identity-webhook` | The cluster's control plane | Mutating admission webhook that configures pods of associated service accounts, including init and sidecar containers |
| `ezgh-pod-identity-agent` | Every node, as a host-network DaemonSet | Serves `GET http://169.254.170.23/v1/credentials` to pods, exchanging each pod's token for a Bot API key |

Logs are structured JSON.

## Container image and binaries

| Artifact | Location |
| --- | --- |
| Agent image (linux/amd64) | `q5zljyjh52il.registry.ezghcloud.com/kubernetes/ezgh-pod-identity-agent:<version>` |
| Binaries (linux/amd64, linux/arm64) with `SHA256SUMS` | [GitHub Releases](https://github.com/ezgamehost/ezgh-pod-identity/releases) |

## Building from source

Requires Go 1.27.1 or later.

```sh
CGO_ENABLED=0 GOOS=linux go build ./cmd/...
docker build .
```

## Testing

| Check | Command |
| --- | --- |
| Unit tests | `go test -race $(go list ./... \| grep -v /internal/kit/)` |
| Lint | `gofmt -l .`, `go vet` and `staticcheck` |
| Logging | `scripts/check-logging.sh` |

Continuous integration runs these and builds the image on every pull request and push to `main`.

## Releases

Releases follow semantic versioning. Pushing a `vX.Y.Z` tag on `main` publishes the container image
and then the binaries. Published versions are immutable; fixes ship as new versions.

## Vendored code

`internal/kit` and `internal/cloud/config` are vendored from shared EZGH libraries and refreshed
with `scripts/sync-kit.sh` and `scripts/sync-copies.sh`. Changes to them are made upstream.

## Security

Please report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

Licensed under the [Apache License 2.0](LICENSE).
