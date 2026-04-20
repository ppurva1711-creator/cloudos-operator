/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrlzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	tasksv1 "github.com/orchestrator/module2-orchestrator/api/v1"
	"github.com/orchestrator/module2-orchestrator/controllers"
	"github.com/orchestrator/module2-orchestrator/pkg/logger"
	"github.com/orchestrator/module2-orchestrator/pkg/utils"
	"go.uber.org/zap"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(tasksv1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var logLevel string
	var env string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error).")
	flag.StringVar(&env, "env", "production", "Environment (development, production).")
	opts := ctrlzap.Options{
		Development: logLevel == "debug",
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(ctrlzap.New(ctrlzap.UseFlagOptions(&opts)))

	// Initialize structured logger
	structuredLogger, err := logger.NewLogger(env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer structuredLogger.Sync()

	// Legacy logger for backward compatibility
	legacyLog := utils.InitializeLogger(logLevel)

	// Log startup information
	hostname, _ := os.Hostname()
	structuredLogger.Infof("Starting CloudTask Operator",
		zap.String("version", "1.0.0"),
		zap.String("env", env),
		zap.String("log_level", logLevel),
		zap.String("hostname", hostname),
		zap.Bool("leader_election", enableLeaderElection),
		zap.String("metrics_addr", metricsAddr),
		zap.String("health_probe_addr", probeAddr),
	)

	// Create manager
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "cloudtask-operator.orchestrator.dev",
	})
	if err != nil {
		structuredLogger.Fatalf("unable to start manager: %v", err)
	}

	structuredLogger.Infof("Kubernetes manager created successfully",
		zap.Bool("leader_election_enabled", enableLeaderElection),
	)

	// Setup CloudTask reconciler
	if err = (&controllers.CloudTaskReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Log:    legacyLog,
	}).SetupWithManager(mgr); err != nil {
		structuredLogger.Fatalf("unable to create controller: %v", err)
	}

	structuredLogger.Infof("CloudTask controller reconciler registered")

	// Setup webhooks if cert manager is installed
	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		if err = (&tasksv1.CloudTask{}).SetupWebhookWithManager(mgr); err != nil {
			structuredLogger.Warnf("unable to create webhook: %v", err)
		} else {
			structuredLogger.Infof("CloudTask webhooks configured")
		}
	}

	// Setup health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		structuredLogger.Fatalf("unable to set up health check: %v", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		structuredLogger.Fatalf("unable to set up ready check: %v", err)
	}

	structuredLogger.Infof("health checks registered successfully")

	structuredLogger.Infof("Starting operator event loop")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		structuredLogger.Fatalf("problem running manager: %v", err)
	}

	structuredLogger.Infof("operator shutdown complete")
}
