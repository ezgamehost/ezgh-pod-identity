package podidentity

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/ezgamehost/ezgh-pod-identity/internal/cloud/config"
)

var assocAppSA = []config.Association{{Namespace: "app", ServiceAccount: "sa"}}

func pod(sa string, containers ...corev1.Container) *corev1.Pod {
	if len(containers) == 0 {
		containers = []corev1.Container{{Name: "main", Image: "app"}}
	}
	return &corev1.Pod{Spec: corev1.PodSpec{ServiceAccountName: sa, Containers: containers}}
}

func env(c corev1.Container) map[string]string {
	m := map[string]string{}
	for _, e := range c.Env {
		m[e.Name] = e.Value
	}
	return m
}

func checkInjected(t *testing.T, c corev1.Container) {
	t.Helper()
	e := env(c)
	if e["EZGH_CONTAINER_CREDENTIALS_FULL_URI"] != "http://169.254.170.23/v1/credentials" ||
		e["EZGH_CONTAINER_AUTHORIZATION_TOKEN_FILE"] != "/var/run/secrets/pods.kubernetes.ezghcloud.com/serviceaccount/token" {
		t.Errorf("%s env: %v", c.Name, e)
	}
	want := corev1.VolumeMount{Name: "ezgh-pod-identity-token", MountPath: "/var/run/secrets/pods.kubernetes.ezghcloud.com/serviceaccount", ReadOnly: true}
	if !reflect.DeepEqual(c.VolumeMounts, []corev1.VolumeMount{want}) {
		t.Errorf("%s mounts: %+v", c.Name, c.VolumeMounts)
	}
}

func TestInjectsWhenAssociated(t *testing.T) {
	p := pod("sa")
	if !Mutate(p, "app", assocAppSA) {
		t.Fatal("want a change")
	}
	checkInjected(t, p.Spec.Containers[0])
	want := []corev1.Volume{{Name: "ezgh-pod-identity-token", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
		Sources: []corev1.VolumeProjection{{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
			Audience: "pods.kubernetes.ezghcloud.com", ExpirationSeconds: ptr.To[int64](86400), Path: "token"}}},
	}}}}
	if !reflect.DeepEqual(p.Spec.Volumes, want) {
		t.Fatalf("volumes: %+v", p.Spec.Volumes)
	}
}

func TestDefaultServiceAccount(t *testing.T) {
	if !Mutate(pod(""), "app", []config.Association{{Namespace: "app", ServiceAccount: "default"}}) {
		t.Fatal("an unnamed service account is default")
	}
}

func TestNoAssociationNoPatch(t *testing.T) {
	for _, assoc := range [][]config.Association{nil, {{Namespace: "other", ServiceAccount: "sa"}}, {{Namespace: "app", ServiceAccount: "other"}}} {
		p := pod("sa")
		if Mutate(p, "app", assoc) || len(p.Spec.Volumes) != 0 || len(p.Spec.Containers[0].Env) != 0 {
			t.Errorf("%v: changed %+v", assoc, p.Spec)
		}
		resp := admit(t, writeConfig(t, assoc), pod("sa"))
		if !resp.Allowed || len(resp.Patches) != 0 {
			t.Errorf("%v: %+v", assoc, resp)
		}
	}
}

func TestKeepsExistingEnv(t *testing.T) {
	p := pod("sa", corev1.Container{Name: "main", Env: []corev1.EnvVar{{Name: "EZGH_CONTAINER_CREDENTIALS_FULL_URI", Value: "http://mine"}}})
	Mutate(p, "app", assocAppSA)
	e := env(p.Spec.Containers[0])
	if e["EZGH_CONTAINER_CREDENTIALS_FULL_URI"] != "http://mine" || len(p.Spec.Containers[0].Env) != 2 || e["EZGH_CONTAINER_AUTHORIZATION_TOKEN_FILE"] == "" {
		t.Fatalf("env: %+v", p.Spec.Containers[0].Env)
	}
}

// The obs agent's chart needs init containers and sidecars injected too.
func TestInitContainersToo(t *testing.T) {
	p := pod("sa", corev1.Container{Name: "main"}, corev1.Container{Name: "second"})
	p.Spec.InitContainers = []corev1.Container{{Name: "init"}, {Name: "sidecar", RestartPolicy: ptr.To(corev1.ContainerRestartPolicyAlways)}}
	Mutate(p, "app", assocAppSA)
	for _, c := range append(p.Spec.InitContainers, p.Spec.Containers...) {
		checkInjected(t, c)
	}
}

func TestIdempotentOnReinvocation(t *testing.T) {
	p := pod("sa")
	p.Spec.InitContainers = []corev1.Container{{Name: "init"}}
	Mutate(p, "app", assocAppSA)
	once := p.DeepCopy()
	if Mutate(p, "app", assocAppSA) || !reflect.DeepEqual(p, once) {
		t.Fatalf("second pass changed the pod: %+v", p.Spec)
	}
	resp := admit(t, writeConfig(t, assocAppSA), once)
	if !resp.Allowed || len(resp.Patches) != 0 {
		t.Fatalf("reinvocation patched: %+v", resp.Patches)
	}
}

func TestAssociationsReadEachAdmission(t *testing.T) {
	path := writeConfig(t, nil)
	if resp := admit(t, path, pod("sa")); len(resp.Patches) != 0 {
		t.Fatalf("no association yet: %+v", resp.Patches)
	}
	writeConfigAt(t, path, assocAppSA)
	resp := admit(t, path, pod("sa"))
	if !resp.Allowed || len(resp.Patches) == 0 {
		t.Fatalf("association added: %+v", resp)
	}
	writeConfigAt(t, path, nil)
	if resp := admit(t, path, pod("sa")); len(resp.Patches) != 0 {
		t.Fatalf("association removed: %+v", resp.Patches)
	}
}

func TestMissingConfigErrors(t *testing.T) {
	resp := admit(t, filepath.Join(t.TempDir(), "missing.json"), pod("sa"))
	if resp.Allowed || resp.Result.Code != 500 {
		t.Fatalf("%+v", resp)
	}
}

func writeConfig(t *testing.T, assoc []config.Association) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cloud.json")
	writeConfigAt(t, path, assoc)
	return path
}

func writeConfigAt(t *testing.T, path string, assoc []config.Association) {
	t.Helper()
	body, _ := json.Marshal(config.Config{Cluster: "kcl_a", ClusterUUID: "u", Org: "o", Project: "p", PodIdentityAssociations: assoc})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func admit(t *testing.T, configPath string, p *corev1.Pod) admission.Response {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return (&Webhook{ConfigPath: configPath}).Handle(context.Background(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		UID: "1", Namespace: "app", Operation: admissionv1.Create, Object: runtime.RawExtension{Raw: raw},
	}})
}
