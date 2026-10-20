package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/GameplanePanel/gameplane/api/internal/gateway"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func runGateway(ctx context.Context, args []string) error {
	var cfg gateway.Config
	var address, namespaces string
	flags := flag.NewFlagSet("gateway", flag.ContinueOnError)
	flags.StringVar(&address, "addr", "127.0.0.1:8443", "private gateway listen address")
	flags.StringVar(&cfg.Cluster, "cluster", "", "registered cluster identity")
	flags.StringVar(&namespaces, "namespaces", "gameplane-games", "comma-separated permitted game namespaces")
	flags.StringVar(&cfg.PeerURI, "peer-uri", "", "exact URI SAN of the trusted central API")
	flags.StringVar(&cfg.TLS.Certificate, "tls-cert", "", "gateway server certificate file")
	flags.StringVar(&cfg.TLS.Key, "tls-key", "", "gateway server key file")
	flags.StringVar(&cfg.TLS.CA, "tls-client-ca", "", "dedicated central peer trust bundle")
	flags.StringVar(&cfg.AgentTLS.Certificate, "agent-client-cert", "", "local agent client certificate")
	flags.StringVar(&cfg.AgentTLS.Key, "agent-client-key", "", "local agent client key")
	flags.StringVar(&cfg.AgentTLS.CA, "agent-ca", "", "local agent CA bundle")
	flags.DurationVar(&cfg.MaxRequestDuration, "max-request-duration", 0, "optional maximum operation or stream lifetime (0 disables the cap; peer certificate expiry still applies)")
	flags.BoolVar(&cfg.CaptureEnabled, "capture-enabled", false, "whether this site's operator enables capture")
	flags.Int64Var(&cfg.CaptureDefaultRetentionSeconds, "capture-default-retention", 86400, "this site's default capture retention in seconds")
	flags.Int64Var(&cfg.CaptureMaxRetentionSeconds, "capture-max-retention", 604800, "this site's maximum capture retention in seconds")
	flags.Int64Var(&cfg.CaptureDefaultMaxDurationSecs, "capture-default-max-duration", 300, "this site's default capture duration in seconds")
	flags.Int64Var(&cfg.CaptureDefaultMaxSizeBytes, "capture-default-max-size", 943718400, "this site's capture size ceiling in bytes")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("gateway flags: %w", err)
	}
	for _, ns := range strings.Split(namespaces, ",") {
		if ns = strings.TrimSpace(ns); ns != "" {
			cfg.Namespaces = append(cfg.Namespaces, ns)
		}
	}
	tlsConfig, err := gateway.ServerTLS(cfg.TLS, cfg.PeerURI)
	if err != nil {
		return fmt.Errorf("gateway TLS: %w", err)
	}
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("gateway kubeconfig: %w", err)
	}
	client, err := kube.New(restConfig)
	if err != nil {
		return fmt.Errorf("gateway kube client: %w", err)
	}
	handler, err := gateway.NewHandler(cfg, client)
	if err != nil {
		return fmt.Errorf("gateway handler: %w", err)
	}
	server := &http.Server{Addr: address, Handler: handler, TLSConfig: tlsConfig, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}
	completed := make(chan error, 1)
	go func() { completed <- server.ListenAndServeTLS("", "") }()
	select {
	case err := <-completed:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve gateway: %w", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shutdown gateway: %w", err)
		}
		return nil
	}
}
