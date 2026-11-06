package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeAPI is kubernetes-api's pod-credentials route: it records each exchange and answers with
// the next scripted response.
type fakeAPI struct {
	mu        sync.Mutex
	exchanges []string // tokens, in order
	answer    func(n int) (status int, retryAfter, body string)
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/organizations/org_a/projects/prj_a/clusters/kcl_a/pod-credentials" ||
		r.Header.Get("Authorization") != "" || r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, r.Method+" "+r.URL.Path, http.StatusTeapot)
		return
	}
	var in map[string]string
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in) != 1 {
		http.Error(w, "body", http.StatusTeapot)
		return
	}
	f.mu.Lock()
	f.exchanges = append(f.exchanges, in["token"])
	n := len(f.exchanges)
	f.mu.Unlock()
	status, retryAfter, body := f.answer(n)
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

const okBody = `{"apiKey":"ezgh_secret","expiresAt":"2026-11-06T01:00:00.000Z","bot":"bot_a"}`

func newAgent(t *testing.T, answer func(n int) (int, string, string)) (*Agent, *fakeAPI) {
	f := &fakeAPI{answer: answer}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &Agent{API: srv.URL, Org: "org_a", Project: "prj_a", Cluster: "kcl_a", HTTP: srv.Client()}, f
}

func fetch(a *Agent, method, path, authorization string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://169.254.170.23"+path, nil)
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}

func always(status int, retryAfter, body string) func(int) (int, string, string) {
	return func(int) (int, string, string) { return status, retryAfter, body }
}

func TestExchangesToken(t *testing.T) {
	a, f := newAgent(t, always(200, "", okBody))
	for _, header := range []string{"Bearer tok", "bearer tok", "tok"} {
		w := fetch(a, "GET", "/v1/credentials", header)
		if w.Code != 200 || w.Body.String() != okBody || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%q: %d %q", header, w.Code, w.Body)
		}
	}
	if strings.Join(f.exchanges, ",") != "tok,tok,tok" {
		t.Fatalf("exchanges %v", f.exchanges)
	}
}

func TestMissingAuthorization400(t *testing.T) {
	a, f := newAgent(t, always(200, "", okBody))
	for _, header := range []string{"", "Bearer ", "Bearer " + strings.Repeat("x", 16<<10+1)} {
		if w := fetch(a, "GET", "/v1/credentials", header); w.Code != 400 {
			t.Errorf("%.20q: %d", header, w.Code)
		}
	}
	if w := fetch(a, "GET", "/v1/credentials", strings.Repeat("x", 16<<10)); w.Code != 200 {
		t.Errorf("16 KiB is allowed: %d", w.Code)
	}
	if len(f.exchanges) != 1 {
		t.Fatalf("exchanges %d", len(f.exchanges))
	}
}

func TestOtherMethodsAndPaths(t *testing.T) {
	a, f := newAgent(t, always(200, "", okBody))
	if w := fetch(a, "POST", "/v1/credentials", "tok"); w.Code != 405 || w.Header().Get("Allow") != "GET" {
		t.Errorf("POST: %d", w.Code)
	}
	if w := fetch(a, "GET", "/v1/credentials/x", "tok"); w.Code != 404 {
		t.Errorf("other path: %d", w.Code)
	}
	if len(f.exchanges) != 0 {
		t.Fatal("no exchange for a refused request")
	}
}

func Test429PassedThrough(t *testing.T) {
	body := `{"error":{"code":"rate_limited","message":"slow down"}}`
	a, _ := newAgent(t, always(429, "7", body))
	w := fetch(a, "GET", "/v1/credentials", "Bearer tok")
	if w.Code != 429 || w.Header().Get("Retry-After") != "7" || w.Body.String() != body {
		t.Fatalf("%d %q %q", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
}

// Review Focus 2: an association deleted while its pods run is refused on the very next fetch.
func TestDeletedAssociationRefusedOnNextFetch(t *testing.T) {
	refused := `{"error":{"code":"unauthenticated","message":"Authentication required"}}`
	a, f := newAgent(t, func(n int) (int, string, string) {
		if n == 1 {
			return 200, "", okBody
		}
		return 401, "", refused
	})
	if w := fetch(a, "GET", "/v1/credentials", "Bearer tok"); w.Code != 200 || !strings.Contains(w.Body.String(), "apiKey") {
		t.Fatalf("first: %d %q", w.Code, w.Body)
	}
	w := fetch(a, "GET", "/v1/credentials", "Bearer tok")
	if w.Code != 401 || strings.Contains(w.Body.String(), "apiKey") || len(f.exchanges) != 2 {
		t.Fatalf("second: %d %q after %d exchanges", w.Code, w.Body, len(f.exchanges))
	}
}

func TestNoCachingBetweenPods(t *testing.T) {
	a, f := newAgent(t, always(200, "", okBody))
	fetch(a, "GET", "/v1/credentials", "Bearer pod-1")
	fetch(a, "GET", "/v1/credentials", "Bearer pod-2")
	if strings.Join(f.exchanges, ",") != "pod-1,pod-2" {
		t.Fatalf("exchanges %v", f.exchanges)
	}
}

func TestUpstreamUnreachable503(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	a := &Agent{API: srv.URL, Org: "org_a", Project: "prj_a", Cluster: "kcl_a", HTTP: http.DefaultClient}
	w := fetch(a, "GET", "/v1/credentials", "Bearer tok")
	if w.Code != 503 || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestRedirectAnsweredAsIs(t *testing.T) {
	var followed bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer elsewhere.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	a := &Agent{API: redirect.URL, Org: "org_a", Project: "prj_a", Cluster: "kcl_a", HTTP: http.DefaultClient}
	if w := fetch(a, "GET", "/v1/credentials", "Bearer tok"); w.Code != 307 || followed {
		t.Fatalf("%d, followed %v", w.Code, followed)
	}
}
