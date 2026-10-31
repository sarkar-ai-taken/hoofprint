// hoofprint-api is the control plane: gRPC endpoint for agents, HTTP API for
// the CLI and UI, Postgres (or in-memory) state.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"github.com/sarkar-ai-taken/hoofprint/internal/api"
	"github.com/sarkar-ai-taken/hoofprint/internal/controlplane"
	"github.com/sarkar-ai-taken/hoofprint/internal/model"
	"github.com/sarkar-ai-taken/hoofprint/internal/store"
	"github.com/sarkar-ai-taken/hoofprint/internal/store/memory"
	"github.com/sarkar-ai-taken/hoofprint/internal/store/postgres"
	"github.com/sarkar-ai-taken/hoofprint/internal/verify"
	"github.com/sarkar-ai-taken/hoofprint/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "hoofprint-api:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("hoofprint-api", flag.ExitOnError)
	httpAddr := fs.String("http", envOr("HOOFPRINT_HTTP_ADDR", ":8080"), "HTTP API listen address")
	grpcAddr := fs.String("grpc", envOr("HOOFPRINT_GRPC_ADDR", ":9090"), "agent gRPC listen address")
	dbURL := fs.String("db", envOr("HOOFPRINT_DATABASE_URL", "memory"), "postgres://... or \"memory\" for an in-memory store (dev only)")
	agentToken := fs.String("agent-token", os.Getenv("HOOFPRINT_AGENT_TOKEN"), "bearer token agents must present (empty disables auth)")
	heartbeat := fs.Duration("heartbeat-interval", 10*time.Second, "heartbeat interval advertised to agents")
	offlineAfter := fs.Duration("offline-after", 30*time.Second, "mark an agent offline after this much silence")
	verifyConfig := fs.String("verify-config", os.Getenv("HOOFPRINT_VERIFY_CONFIG"), "YAML file declaring the metrics to compare (enables the verification gate)")
	verifyWindow := fs.Duration("verify-window", 0, "override the config's comparison window")
	verifySettle := fs.Duration("verify-settle", 0, "override the config's settle time before the post window")
	verifyStep := fs.Duration("verify-step", 0, "override the config's sample step")
	logLevel := fs.String("log-level", envOr("HOOFPRINT_LOG_LEVEL", "info"), "debug|info|warn|error")
	_ = fs.Parse(os.Args[1:])

	log := newLogger(*logLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var st store.Store
	if *dbURL == "memory" {
		log.Warn("using in-memory store; state is lost on restart")
		st = memory.New()
	} else {
		pg, err := postgres.Open(ctx, *dbURL)
		if err != nil {
			return err
		}
		st = pg
		log.Info("connected to postgres")
	}
	defer st.Close()

	reg := controlplane.NewRegistry(st, log, *heartbeat, *offlineAfter)
	disp := controlplane.NewDispatcher(reg)

	onDeploy := func(_ context.Context, d *model.Deploy) {
		log.Info("deploy observed, no verification gate configured (--verify-config)", "deploy", d.ID, "tier", d.Tier.String())
	}
	if *verifyConfig != "" {
		cfg, err := verify.LoadConfig(*verifyConfig)
		if err != nil {
			return err
		}
		if *verifyWindow > 0 {
			cfg.Window = *verifyWindow
		}
		if *verifySettle > 0 {
			cfg.Settle = *verifySettle
		}
		if *verifyStep > 0 {
			cfg.Step = *verifyStep
		}
		v := verify.New(st, disp, cfg, log)
		onDeploy = v.HandleDeploy
		log.Info("verification gate enabled", "metrics", len(cfg.Metrics), "window", cfg.Window, "settle", cfg.Settle, "step", cfg.Step)
	}
	cp := controlplane.NewServer(st, reg, disp, log,
		controlplane.WithAgentToken(*agentToken),
		controlplane.WithDeployHandler(onDeploy))

	gsrv := grpc.NewServer(grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 10 * time.Second}))
	cp.Register(gsrv)
	glis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		return fmt.Errorf("listen grpc: %w", err)
	}
	hsrv := &http.Server{Addr: *httpAddr, Handler: api.New(st, disp, log, version.Version).Handler(), ReadHeaderTimeout: 10 * time.Second}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { reg.RunReaper(gctx); return nil })
	g.Go(func() error {
		log.Info("agent gRPC listening", "addr", glis.Addr().String())
		return gsrv.Serve(glis)
	})
	g.Go(func() error {
		log.Info("HTTP API listening", "addr", *httpAddr, "version", version.Version)
		if err := hsrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	g.Go(func() error {
		<-gctx.Done()
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hsrv.Shutdown(shutdownCtx)
		gsrv.GracefulStop()
		return nil
	})
	return g.Wait()
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
