// Command ezgh-pod-identity-webhook serves https://127.0.0.1:9445/mutate-pods on every
// control-plane VM (each apiserver calls its own loopback), so it has no Lease and needs no API
// access. It gives pods whose service account has a pod identity association the token volume
// and EZGH_CONTAINER_* variables the node agent answers. The serving certificate is the local
// agent's (/run/ezgh/webhook), reloaded when it is rewritten. EZGH_CLOUD_CONFIG overrides
// /etc/ezgh/cloud.json. It runs as systemd unit ezgh-cloud@pod-identity-webhook.
package main

import (
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/ezgamehost/ezgh-pod-identity/internal/cloud/config"
	"github.com/ezgamehost/ezgh-pod-identity/internal/cloud/podidentity"
	"github.com/ezgamehost/ezgh-pod-identity/internal/kit/httpapi"
	"github.com/ezgamehost/ezgh-pod-identity/internal/kit/logging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ezgh-pod-identity-webhook:", err)
		os.Exit(1)
	}
}

func run() error {
	// The service name ezgh-cloud's subcommand logged under, kept so log queries still match.
	log := logging.New(os.Stdout, "ezgh-cloud-pod-identity-webhook", os.Getenv("APP_ENV") == "local")
	klog.SetSlogLogger(log)
	ctrl.SetLogger(logr.FromSlogHandler(log.Handler()))
	path := config.Path
	if p := os.Getenv("EZGH_CLOUD_CONFIG"); p != "" {
		path = p
	}
	srv := webhook.NewServer(webhook.Options{Host: "127.0.0.1", Port: 9445, CertDir: "/run/ezgh/webhook"})
	srv.Register("/mutate-pods", httpapi.AccessLog(log, &admission.Webhook{Handler: &podidentity.Webhook{ConfigPath: path}}))
	return srv.Start(ctrl.SetupSignalHandler())
}
