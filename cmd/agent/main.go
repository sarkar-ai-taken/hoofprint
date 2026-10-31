// hoofprint-agent runs inside a target cluster, connects outbound to the
// control plane, watches workloads and executes work items (ADR-0004).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/sarkar-ai-taken/hoofprint/internal/agent"
	"github.com/sarkar-ai-taken/hoofprint/internal/agent/capabilities"
	"github.com/sarkar-ai-taken/hoofprint/internal/agent/watcher"
	agentv1 "github.com/sarkar-ai-taken/hoofprint/internal/agentproto/hoofprint/agent/v1"
	"github.com/sarkar-ai-taken/hoofprint/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "hoofprint-agent:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("hoofprint-agent", flag.ExitOnError)
	controlPlane := fs.String("control-plane", envOr("HOOFPRINT_CONTROL_PLANE", "localhost:9090"), "control plane gRPC host:port")
	token := fs.String("token", os.Getenv("HOOFPRINT_AGENT_TOKEN"), "agent bearer token")
	cluster := fs.String("cluster", envOr("HOOFPRINT_CLUSTER", ""), "cluster name (required)")
	agentID := fs.String("agent-id", os.Getenv("HOOFPRINT_AGENT_ID"), "stable agent id (default agent-<cluster>)")
	tags := fs.String("tags", os.Getenv("HOOFPRINT_TAGS"), "selector tags, k=v,k2=v2")
	insecure := fs.Bool("insecure", envOr("HOOFPRINT_INSECURE", "false") == "true", "disable TLS to the control plane")
	tlsServerName := fs.String("tls-server-name", "", "expected TLS server name")
	kubeconfig := fs.String("kubeconfig", os.Getenv("KUBECONFIG"), "kubeconfig path (in-cluster config when empty)")
	namespaces := fs.String("namespaces", os.Getenv("HOOFPRINT_NAMESPACES"), "comma-separated namespaces to watch (all when empty)")
	prometheusURL := fs.String("prometheus-url", os.Getenv("HOOFPRINT_PROMETHEUS_URL"), "Prometheus base URL reachable from the agent (adds the prometheus capability)")
	logLevel := fs.String("log-level", envOr("HOOFPRINT_LOG_LEVEL", "info"), "debug|info|warn|error")
	_ = fs.Parse(os.Args[1:])

	if *cluster == "" {
		return fmt.Errorf("--cluster is required")
	}
	log := newLogger(*logLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	restCfg, err := loadKubeConfig(*kubeconfig)
	if err != nil {
		return err
	}
	restCfg.UserAgent = "hoofprint-agent/" + version.Version
	kube, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return err
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return err
	}

	var extra []string
	if *prometheusURL != "" {
		extra = append(extra, capabilities.Prometheus)
	}
	caps := capabilities.Detect(kube.Discovery(), extra...)
	log.Info("detected capabilities", "capabilities", caps)

	a := agent.New(agent.Config{
		ControlPlane: *controlPlane, Token: *token, AgentID: *agentID, Cluster: *cluster, Version: version.Version,
		Capabilities: caps, Tags: parseTags(*tags), Insecure: *insecure, TLSServerName: *tlsServerName, Log: log,
	})
	a.RegisterKubernetesHandlers(kube, dyn, caps)
	if *prometheusURL != "" {
		a.RegisterPrometheusHandler(*prometheusURL, nil)
	}

	w := watcher.New(kube, dyn, watcher.Options{
		Cluster:       *cluster,
		WatchRollouts: capabilities.Has(caps, capabilities.ArgoRollouts),
		WatchCanaries: capabilities.Has(caps, capabilities.Flagger),
		Namespaces:    splitNonEmpty(*namespaces),
		Resync:        10 * time.Minute,
		Log:           log,
	}, func(ev *agentv1.DeployEvent) {
		log.Info("deploy observed", "ns", ev.Namespace, "kind", ev.Kind, "name", ev.Name, "tier", ev.Tier.String(), "rev", ev.RevisionBefore+".."+ev.RevisionAfter)
		a.EmitDeploy(ev)
	})

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return a.Run(gctx) })
	g.Go(func() error { return w.Run(gctx) })
	return g.Wait()
}

func loadKubeConfig(path string) (*rest.Config, error) {
	if path == "" {
		if cfg, err := rest.InClusterConfig(); err == nil {
			return cfg, nil
		}
		path = clientcmd.RecommendedHomeFile
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, fmt.Errorf("kubeconfig: %w", err)
	}
	return cfg, nil
}

func parseTags(s string) map[string]string {
	out := map[string]string{}
	for _, kv := range splitNonEmpty(s) {
		k, v, _ := strings.Cut(kv, "=")
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}
