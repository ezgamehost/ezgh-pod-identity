// Package agent is the pod identity add-on's node agent, apart from the webhook (podidentity) so
// its binary doesn't link controller-runtime.
package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/ezgamehost/ezgh-pod-identity/internal/kit/httpapi"
)

// CredentialsPath is the agent's one route.
const CredentialsPath = "/v1/credentials"

const maxToken = 16 << 10

// Agent serves GET /v1/credentials on the node: it exchanges the pod's projected token
// (Authorization: Bearer <token>, or the bare token) at kubernetes-api's pod-credentials route and
// answers kubernetes-api's status, body and Retry-After unchanged. It keeps nothing: every request
// is a fresh exchange, so a deleted association or pod is refused on the next fetch (Review Focus 2).
type Agent struct {
	API                   string // https://kubernetes-api.ezghcloud.com
	Org, Project, Cluster string
	HTTP                  *http.Client
}

func (a *Agent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != CredentialsPath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := r.Header.Get("Authorization")
	if len(token) >= 7 && strings.EqualFold(token[:7], "bearer ") {
		token = token[7:]
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxToken {
		httpapi.WriteError(w, httpapi.InvalidRequest("Authorization must hold the pod's identity token (at most 16 KiB)"))
		return
	}
	body, _ := json.Marshal(map[string]string{"token": token})
	url := a.API + "/v1/organizations/" + a.Org + "/projects/" + a.Project + "/clusters/" + a.Cluster + "/pod-credentials"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		httpapi.WriteError(w, httpapi.Internal(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// A redirect is answered as-is: following it would send the pod's token wherever it points.
	client := *a.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		e := httpapi.Unavailable("kubernetes-api is unreachable", err)
		e.RetryAfter = 1
		httpapi.WriteError(w, e)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Retry-After", "Location"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Cache-Control", "no-store") // an API key: nothing on the way may keep it
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
