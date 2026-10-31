package main

import (
	"context"
	"crypto/tls"
	"flag"
	"os"
	"time"

	// Lets the manager use kubeconfigs that sign in through OIDC or a cloud provider.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	temporalv1alpha1 "github.com/cujarrett/temporal-controller/api/v1alpha1"
	"github.com/cujarrett/temporal-controller/internal/controller"
	"github.com/cujarrett/temporal-controller/internal/temporal"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(temporalv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var webhookPort int
	var enableLeaderElection bool
	var probeAddr string
	var temporalAddress string
	var entraTenantID, entraClientID, temporalTokenScope, spiffeSocket string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&temporalAddress, "temporal-address", "localhost:7233", "The host:port of the Temporal frontend.")
	flag.StringVar(&entraTenantID, "entra-tenant-id", "",
		"Entra tenant to get Temporal tokens from. Empty sends no token.")
	flag.StringVar(&entraClientID, "entra-client-id", "", "The controller's Entra app client ID.")
	flag.StringVar(&temporalTokenScope, "temporal-token-scope", "",
		"The Entra scope for Temporal, such as api://<tenant>/platform-temporal/.default.")
	flag.StringVar(&spiffeSocket, "spiffe-socket", "/var/run/secrets/spiffe.io/api.sock",
		"The SPIRE agent's Workload API socket.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "Port the webhook server listens on. "+
		"Defaults to 9443. Set -1 to disable the webhook server.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// HTTP/2 is off by default because of the Stream Cancellation and Rapid Reset CVEs.
	// See GHSA-qppj-fm5r-hxr3 and GHSA-4374-p667-p6c8.
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
		Port:    webhookPort,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// Only callers that Kubernetes authenticates and authorizes can read metrics.
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// With no cert path, controller-runtime serves metrics with a self-signed cert.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "038905e8.local.lab",
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	// A nil provider sends no token, so local runs and tests need no SPIRE or Entra.
	var headers temporal.Headers
	if entraTenantID != "" && entraClientID != "" && temporalTokenScope != "" {
		token := &temporal.EntraToken{
			TenantID:  entraTenantID,
			ClientID:  entraClientID,
			Scope:     temporalTokenScope,
			FetchSVID: temporal.SPIRESVID(spiffeSocket),
		}
		headers = token
		// One fetch at startup proves the SVID to Entra trade works. Failure is logged, not fatal,
		// because each Temporal call fetches again and the SPIRE agent can start after this pod.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if _, err := token.Token(ctx); err != nil {
			setupLog.Error(err, "Could not fetch an Entra token for Temporal yet")
		} else {
			setupLog.Info("Fetched an Entra token for Temporal")
		}
		cancel()
	}

	temporalClient, err := temporal.New(temporalAddress, headers)
	if err != nil {
		setupLog.Error(err, "Failed to create Temporal client")
		os.Exit(1)
	}
	defer temporalClient.Close()

	if err := (&controller.TemporalNamespaceReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Temporal: temporalClient,
		Recorder: mgr.GetEventRecorder("temporal-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "temporalnamespace")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
