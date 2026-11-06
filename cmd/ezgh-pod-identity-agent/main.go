// Command ezgh-pod-identity-agent is the pod identity add-on's node agent: a host-network
// DaemonSet serving GET http://169.254.170.23/v1/credentials to pods (agent.Agent). The VPC
// CNI's ipamd (ezgamehost/ezgh-cni) holds the address on the dummy link ezgh-pia; the agent
// only binds it.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ezgamehost/ezgh-pod-identity/internal/cloud/podidentity/agent"
	"github.com/ezgamehost/ezgh-pod-identity/internal/kit/httpapi"
	"github.com/ezgamehost/ezgh-pod-identity/internal/kit/logging"
)

const addr = "169.254.170.23:80"

func main() {
	api := flag.String("api", "https://kubernetes-api.ezghcloud.com", "kubernetes-api's base URL")
	org := flag.String("org", "", "the cluster's organization")
	project := flag.String("project", "", "the cluster's project")
	cluster := flag.String("cluster", "", "the cluster (kcl_…)")
	flag.Parse()
	log := logging.New(os.Stdout, "ezgh-pod-identity-agent", os.Getenv("APP_ENV") == "local")
	if *org == "" || *project == "" || *cluster == "" {
		log.Error("-org, -project and -cluster are required")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	pia := &agent.Agent{API: *api, Org: *org, Project: *project, Cluster: *cluster, HTTP: &http.Client{Timeout: 10 * time.Second}}
	if err := serve(ctx, addr, pia, log); err != nil {
		log.Error("serving failed", "error", err.Error())
		os.Exit(1)
	}
}

// serve binds addr, retrying until ipamd has put the address on the node, then serves h until ctx
// is done and drains requests in flight.
func serve(ctx context.Context, addr string, h http.Handler, log *slog.Logger) error {
	ln, err := listen(ctx, addr, log)
	if err != nil {
		return nil // ctx done before the address appeared
	}
	mux := http.NewServeMux()
	mux.Handle(agent.CredentialsPath, h) // gives the access line its route
	srv := &http.Server{Handler: httpapi.AccessLog(log, mux), ReadHeaderTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		drain, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = srv.Shutdown(drain)
	}()
	log.Info("listening", "addr", addr)
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// listen retries with backoff (1 s doubling to 30 s) instead of exiting into a crash loop while
// the address isn't on the node yet.
func listen(ctx context.Context, addr string, log *slog.Logger) (net.Listener, error) {
	var lc net.ListenConfig
	for wait := time.Second; ; wait = min(2*wait, 30*time.Second) {
		ln, err := lc.Listen(ctx, "tcp4", addr)
		if err == nil {
			return ln, nil
		}
		log.Warn("can't listen yet, retrying", "addr", addr, "error", err.Error(), "retrySeconds", wait.Seconds())
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}
