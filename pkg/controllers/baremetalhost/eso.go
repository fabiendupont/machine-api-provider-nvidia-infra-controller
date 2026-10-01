// SPDX-FileCopyrightText: Copyright (c) 2026 Red Hat, Inc. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package baremetalhost

import (
	"context"
	"fmt"
	"time"

	esov1beta1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ESOSyncConfig holds configuration for syncing ESO ExternalSecrets alongside BMH CRs.
type ESOSyncConfig struct {
	// ClusterSecretStoreName is the name of the ClusterSecretStore that ESO uses
	// to pull BMC credentials (e.g. "vault-backend" created by infra-site chart).
	ClusterSecretStoreName string

	// VaultSecretPath is the Vault KV path holding BMC credentials
	// (e.g. "secrets/bmc/default" for factory-default shared credentials).
	VaultSecretPath string

	// RefreshInterval is how often ESO re-reads the secret from the store.
	// Defaults to 1 hour when zero.
	RefreshInterval time.Duration
}

// Enabled reports whether ESO sync is configured (both store and path must be set).
func (c ESOSyncConfig) Enabled() bool {
	return c.ClusterSecretStoreName != "" && c.VaultSecretPath != ""
}

func (r *Reconciler) syncExternalSecret(ctx context.Context, machineID string) error {
	if !r.ESOConfig.Enabled() {
		return nil
	}

	desired := machineToExternalSecret(machineID, r.Namespace, r.Config, r.ESOConfig)

	existing := &esov1beta1.ExternalSecret{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	} else if err != nil {
		return fmt.Errorf("get ExternalSecret: %w", err)
	}

	existing.Spec = desired.Spec
	return r.Update(ctx, existing)
}

func machineToExternalSecret(
	machineID string,
	namespace string,
	cfg BMHSyncConfig,
	eso ESOSyncConfig,
) *esov1beta1.ExternalSecret {
	secretName := fmt.Sprintf(cfg.BMCCredentialsSecretTemplate, machineID)

	refresh := eso.RefreshInterval
	if refresh == 0 {
		refresh = time.Hour
	}

	return &esov1beta1.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				nicoMachineIDLabel: machineID,
			},
		},
		Spec: esov1beta1.ExternalSecretSpec{
			RefreshInterval: &metav1.Duration{Duration: refresh},
			SecretStoreRef: esov1beta1.SecretStoreRef{
				Name: eso.ClusterSecretStoreName,
				Kind: "ClusterSecretStore",
			},
			Target: esov1beta1.ExternalSecretTarget{
				Name:           secretName,
				CreationPolicy: esov1beta1.CreatePolicyOwner,
			},
			Data: []esov1beta1.ExternalSecretData{
				{
					SecretKey: "username",
					RemoteRef: esov1beta1.ExternalSecretDataRemoteRef{
						Key:      eso.VaultSecretPath,
						Property: "username",
					},
				},
				{
					SecretKey: "password",
					RemoteRef: esov1beta1.ExternalSecretDataRemoteRef{
						Key:      eso.VaultSecretPath,
						Property: "password",
					},
				},
			},
		},
	}
}
