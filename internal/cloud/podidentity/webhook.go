// Package podidentity is pod identity on EZGH Bots: the mutating webhook that gives pods of an
// associated service account a projected token and the two EZGH_CONTAINER_* variables. The node
// agent that exchanges that token for a Bot key on each request is package agent.
package podidentity

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/ezgamehost/ezgh-pod-identity/internal/cloud/config"
)

const (
	volumeName = "ezgh-pod-identity-token"
	mountPath  = "/var/run/secrets/pods.kubernetes.ezghcloud.com/serviceaccount"
	// Audience is the projected token's audience, which kubernetes-api's TokenReview requires.
	Audience = "pods.kubernetes.ezghcloud.com"
)

var injectedEnv = []corev1.EnvVar{
	{Name: "EZGH_CONTAINER_CREDENTIALS_FULL_URI", Value: "http://169.254.170.23/v1/credentials"},
	{Name: "EZGH_CONTAINER_AUTHORIZATION_TOKEN_FILE", Value: mountPath + "/token"},
}

// Mutate injects the token volume, its mount and the variables into pod (in namespace) when its
// service account has an association, and reports whether anything changed. Running it again
// changes nothing; a variable or mount path the pod already sets is kept.
// shortcut: a variable the pod sets only through envFrom is overridden; reading ConfigMaps and
// Secrets here would need API access, add it if a customer sets EZGH_CONTAINER_* that way.
func Mutate(pod *corev1.Pod, namespace string, assoc []config.Association) bool {
	sa := pod.Spec.ServiceAccountName
	if sa == "" {
		sa = "default"
	}
	if !slices.Contains(assoc, config.Association{Namespace: namespace, ServiceAccount: sa}) {
		return false
	}
	changed := false
	if !slices.ContainsFunc(pod.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == volumeName }) {
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{Name: volumeName, VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
				Audience: Audience, ExpirationSeconds: ptr.To[int64](86400), Path: "token",
			}}}},
		}})
		changed = true
	}
	for _, cs := range [][]corev1.Container{pod.Spec.InitContainers, pod.Spec.Containers} {
		for i := range cs {
			changed = inject(&cs[i]) || changed
		}
	}
	return changed
}

func inject(c *corev1.Container) bool {
	changed := false
	if !slices.ContainsFunc(c.VolumeMounts, func(m corev1.VolumeMount) bool { return m.MountPath == mountPath }) {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: volumeName, MountPath: mountPath, ReadOnly: true})
		changed = true
	}
	for _, e := range injectedEnv {
		if !slices.ContainsFunc(c.Env, func(have corev1.EnvVar) bool { return have.Name == e.Name }) {
			c.Env = append(c.Env, e)
			changed = true
		}
	}
	return changed
}

// Webhook is the admission handler for pod CREATE. It reads cloud.json's associations on every
// admission, so adding or deleting an association needs no restart.
type Webhook struct {
	ConfigPath string
}

// Handle answers one admission review with a JSON patch, or none.
func (w *Webhook) Handle(_ context.Context, req admission.Request) admission.Response {
	cfg, err := config.Load(w.ConfigPath)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	if !Mutate(&pod, req.Namespace, cfg.PodIdentityAssociations) {
		return admission.Allowed("")
	}
	out, err := json.Marshal(&pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, out)
}
