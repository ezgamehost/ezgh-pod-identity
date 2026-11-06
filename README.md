# ezgh-pod-identity

Pod identity for EZGH Kubernetes, the counterpart of eks-pod-identity-agent and
amazon-eks-pod-identity-webhook: a pod whose service account has a pod identity association gets
short-lived EZGH API keys for that association's Bot, with no secret stored in the cluster. The
EZGH SDKs and CLI read them through `EZGH_CONTAINER_CREDENTIALS_FULL_URI` and
`EZGH_CONTAINER_AUTHORIZATION_TOKEN_FILE`.

| Binary | Runs | Job |
| --- | --- | --- |
| `ezgh-pod-identity-webhook` | each control-plane VM, systemd unit `ezgh-cloud@pod-identity-webhook`, `https://127.0.0.1:9445/mutate-pods` | Mutating webhook: gives pods of associated service accounts (init containers and sidecars too) a projected token (audience `pods.kubernetes.ezghcloud.com`) and the two variables. It reads the associations from `/etc/ezgh/cloud.json` on each admission (`EZGH_CLOUD_CONFIG` overrides the path) and serves the certificate k8sagent (the control-plane VM's agent) writes to `/run/ezgh/webhook` |
| `ezgh-pod-identity-agent` | every node, the pod-identity-agent add-on's host-network DaemonSet, `GET http://169.254.170.23/v1/credentials` | Exchanges the pod's token for a Bot key at kubernetes-api's `…/clusters/<kcl_>/pod-credentials` on every request: nothing is kept, redirects are never followed, 429 and 503 pass through with `Retry-After` |

Logs are JSON lines through the vendored kit (`internal/kit/logging`).

## Build and test

Go 1.27.1 (`go.mod`).

| Check | Command |
| --- | --- |
| Build | `CGO_ENABLED=0 GOOS=linux go build ./cmd/...` |
| Image | `docker build .` (the agent on distroless) |
| Tests | `go test -race $(go list ./... \| grep -v /internal/kit/)` |
| Lint | `gofmt -l .`; `go vet` and `go run honnef.co/go/tools/cmd/staticcheck@v0.8.1` over `$(go list ./... \| grep -v /internal/kit/)` (service-template checks the kit) |
| Logging | `scripts/check-logging.sh` |

CI (`.github/workflows/ci.yml`) runs them and builds the image, without pushing it, on every pull
request and push to main.

## Releases

Push a tag `vX.Y.Z` on main. `.github/workflows/release.yml` publishes the GitHub release
(`ezgh-pod-identity-agent-linux-{amd64,arm64}`, `ezgh-pod-identity-webhook-linux-{amd64,arm64}`,
`SHA256SUMS`) and pushes the linux/amd64 agent image to the kubernetes-addons Registry as
`<host>/kubernetes/ezgh-pod-identity-agent:<tag>`. The image goes first and the binaries only after it, from GitHub Environment `release`
(limited to tags `v*`): secret `KUBERNETES_ADDONS_REGISTRY_KEY` (a platform-organization Bot key that can push to project
kubernetes-addons) and variable `KUBERNETES_ADDONS_REGISTRY_HOST` (`<hostId>.registry.ezghcloud.com`).
A published tag never moves: a fix ships as a new version. The add-on bundle runs the image's
commands and flags as they are: changing one needs a new bundle version in kubernetes-stack too.

kubernetes-stack consumes both: the `pod-identity-agent` add-on bundle (`addons/pod-identity-agent/<version>`)
renders the agent image at its version, and the control-plane payload pins
`ezgh-pod-identity-webhook` by version and SHA-256 in `deploy/images/releases.txt` (a systemd drop-in
points `ezgh-cloud@pod-identity-webhook` at this binary).

## Copied code

Other repositories' code is copied in, not imported, each copy with its own sync:

| Path | Source | Sync |
| --- | --- | --- |
| `internal/kit` | ezgamehost/service-template `kit/go` (commit in `internal/kit/KIT_VERSION`) | `scripts/sync-kit.sh` |
| `internal/cloud/config` (the `cloud.json` reader) | ezgamehost/ezgh-cloud-controller (commit in `COPIES`) | `scripts/sync-copies.sh ezgh-cloud-controller` |

## License

Apache-2.0 ([LICENSE](LICENSE)). Security reports: [SECURITY.md](SECURITY.md).
