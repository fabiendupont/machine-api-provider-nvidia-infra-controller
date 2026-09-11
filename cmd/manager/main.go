/*
Copyright 2026 Fabien Dupont.

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
	"context"
	"flag"
	"os"

	metal3 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/actuators/machine"
	nicov1beta1 "github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/apis/nicoprovider/v1beta1"
	bmhcontroller "github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/controllers/baremetalhost"
	machinecontroller "github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/controllers/machine"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = machinev1beta1.AddToScheme(scheme)
	_ = nicov1beta1.AddToScheme(scheme)
	_ = metal3.AddToScheme(scheme)
}

func main() {
	var metricsAddr string
	var probeAddr string
	var webhookPort int
	var enableLeaderElection bool
	var enableWebhooks bool
	var webhookCertDir string
	var enableBMHSync bool
	var bmhCredentialsSecretName string
	var bmhCredentialsSecretNamespace string
	var bmhNamespace string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080",
		"The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"The address the probe endpoint binds to.")
	flag.IntVar(&webhookPort, "webhook-port", 9443,
		"The port the webhook server binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&enableWebhooks, "enable-webhooks", true,
		"Enable validating webhooks. Requires TLS certificates "+
			"to be provisioned (e.g. via cert-manager).")
	flag.StringVar(&webhookCertDir, "webhook-cert-dir",
		"/tmp/k8s-webhook-server/serving-certs",
		"Directory containing TLS certificates for the webhook server.")
	flag.BoolVar(&enableBMHSync, "enable-bmh-sync", false,
		"Enable BareMetalHost and HostFirmwareComponents sync from NICo machines. "+
			"Requires provider-admin credentials.")
	flag.StringVar(&bmhCredentialsSecretName, "bmh-credentials-secret-name", "nico-credentials",
		"Name of the Secret containing NICo API credentials for BMH sync.")
	flag.StringVar(&bmhCredentialsSecretNamespace, "bmh-credentials-secret-namespace", "openshift-machine-api",
		"Namespace of the NICo credentials Secret for BMH sync.")
	flag.StringVar(&bmhNamespace, "bmh-namespace", "openshift-machine-api",
		"Namespace in which BareMetalHost and HostFirmwareComponents CRs are created.")

	opts := zap.Options{
		Development: false,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgrOpts := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "machine-controller.nico.nvidia.com",
	}

	// Only configure the webhook server when webhooks are enabled
	// and TLS certificates are available.
	if enableWebhooks {
		if _, err := os.Stat(webhookCertDir); os.IsNotExist(err) {
			setupLog.Info(
				"Webhook cert directory not found, disabling webhooks",
				"certDir", webhookCertDir,
			)
			enableWebhooks = false
		} else {
			mgrOpts.WebhookServer = webhook.NewServer(webhook.Options{
				Port:    webhookPort,
				CertDir: webhookCertDir,
			})
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Create the actuator
	actuator := machine.NewActuator(
		mgr.GetClient(),
		mgr.GetEventRecorder("nico-machine-controller"),
	)

	// Setup Machine reconciler
	if err = machinecontroller.SetupMachineController(mgr, actuator); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Machine")
		os.Exit(1)
	}

	// MachineSet reconciliation is handled by the machine-api-operator.
	// This provider only needs to implement the Machine actuator interface.

	// Register validating webhook for Machine objects
	if enableWebhooks {
		mgr.GetWebhookServer().Register(
			"/validate-machine",
			&admission.Webhook{
				Handler: &nicov1beta1.MachineValidator{
					Decoder: admission.NewDecoder(scheme),
				},
			},
		)
		setupLog.Info("Webhooks enabled", "port", webhookPort)
	} else {
		setupLog.Info("Webhooks disabled")
	}

	// Setup BareMetalHost sync controller if enabled
	if enableBMHSync {
		nicoClient, orgName, err := buildBMHNicoClient(mgr, bmhCredentialsSecretName, bmhCredentialsSecretNamespace)
		if err != nil {
			setupLog.Error(err, "unable to read BMH credentials secret",
				"secret", bmhCredentialsSecretNamespace+"/"+bmhCredentialsSecretName)
			os.Exit(1)
		}
		if err := bmhcontroller.SetupWithManager(mgr, nicoClient, orgName, bmhNamespace); err != nil {
			setupLog.Error(err, "unable to set up BMH sync controller")
			os.Exit(1)
		}
		setupLog.Info("BMH sync enabled", "namespace", bmhNamespace)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// buildBMHNicoClient reads the NICo credentials secret via the API reader
// (non-cached, works before the manager cache starts) and returns a NICo client
// and orgName for use by the BMH sync controller.
func buildBMHNicoClient(
	mgr ctrl.Manager,
	secretName, secretNamespace string,
) (machine.NicoClientInterface, string, error) {
	secret := &corev1.Secret{}
	if err := mgr.GetAPIReader().Get(
		context.Background(),
		client.ObjectKey{Name: secretName, Namespace: secretNamespace},
		secret,
	); err != nil {
		return nil, "", err
	}
	endpoint := string(secret.Data["endpoint"])
	orgName := string(secret.Data["orgName"])
	token := string(secret.Data["token"])
	return machine.NewNicoAPIClient(endpoint, token), orgName, nil
}
