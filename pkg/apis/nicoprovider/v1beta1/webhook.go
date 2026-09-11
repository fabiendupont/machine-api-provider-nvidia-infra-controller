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

package v1beta1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// MachineValidator validates Machine objects with NicoMachineProviderSpec.
type MachineValidator struct {
	Decoder admission.Decoder
}

// Handle validates the Machine admission request
func (v *MachineValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	machine := &machinev1beta1.Machine{}
	if err := v.Decoder.Decode(req, machine); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("failed to decode machine: %w", err))
	}

	// Extract provider spec
	if machine.Spec.ProviderSpec.Value == nil {
		return admission.Allowed("no providerSpec to validate")
	}

	providerSpec := &NicoMachineProviderSpec{}
	if err := json.Unmarshal(machine.Spec.ProviderSpec.Value.Raw, providerSpec); err != nil {
		return admission.Denied(fmt.Sprintf("failed to unmarshal providerSpec: %v", err))
	}

	// Skip validation if this is not a NICo provider spec (no required fields set)
	if providerSpec.SiteID == "" && providerSpec.TenantID == "" && providerSpec.VpcID == "" &&
		providerSpec.SubnetID == "" && providerSpec.InstanceTypeID == "" && providerSpec.MachineID == "" {
		return admission.Allowed("not a NICo provider spec")
	}

	// Validate required fields
	if err := validateProviderSpec(providerSpec); err != nil {
		return admission.Denied(err.Error())
	}

	// Validate UUID format for ID fields
	if err := validateUUIDs(providerSpec); err != nil {
		return admission.Denied(err.Error())
	}

	// Validate immutability on update
	if req.OldObject.Raw != nil {
		oldMachine := &machinev1beta1.Machine{}
		if err := v.Decoder.DecodeRaw(req.OldObject, oldMachine); err != nil {
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("failed to decode old machine: %w", err))
		}

		if oldMachine.Spec.ProviderSpec.Value != nil {
			oldSpec := &NicoMachineProviderSpec{}
			if err := json.Unmarshal(oldMachine.Spec.ProviderSpec.Value.Raw, oldSpec); err == nil {
				if err := validateImmutableFields(oldSpec, providerSpec); err != nil {
					return admission.Denied(err.Error())
				}
			}
		}
	}

	return admission.Allowed("valid provider spec")
}

func validateProviderSpec(spec *NicoMachineProviderSpec) error {
	if spec.InstanceTypeID != "" && spec.MachineID != "" {
		return fmt.Errorf("specify either instanceTypeId or machineId, not both")
	}
	if spec.InstanceTypeID == "" && spec.MachineID == "" {
		return fmt.Errorf("either instanceTypeId or machineId is required")
	}
	if spec.SiteID == "" {
		return fmt.Errorf("siteId is required")
	}
	if spec.TenantID == "" {
		return fmt.Errorf("tenantId is required")
	}
	if spec.VpcID == "" {
		return fmt.Errorf("vpcId is required")
	}
	if spec.SubnetID == "" {
		return fmt.Errorf("subnetId is required")
	}
	return nil
}

func validateUUIDs(spec *NicoMachineProviderSpec) error {
	fields := map[string]string{
		"siteId":   spec.SiteID,
		"tenantId": spec.TenantID,
		"vpcId":    spec.VpcID,
		"subnetId": spec.SubnetID,
	}
	for name, value := range fields {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("invalid UUID for %s: %s", name, value)
		}
	}

	if spec.InstanceTypeID != "" {
		if _, err := uuid.Parse(spec.InstanceTypeID); err != nil {
			return fmt.Errorf("invalid UUID for instanceTypeId: %s", spec.InstanceTypeID)
		}
	}
	if spec.MachineID != "" {
		if _, err := uuid.Parse(spec.MachineID); err != nil {
			return fmt.Errorf("invalid UUID for machineId: %s", spec.MachineID)
		}
	}
	if spec.OperatingSystemID != "" {
		if _, err := uuid.Parse(spec.OperatingSystemID); err != nil {
			return fmt.Errorf("invalid UUID for operatingSystemId: %s", spec.OperatingSystemID)
		}
	}
	if spec.NetworkSecurityGroupID != "" {
		if _, err := uuid.Parse(spec.NetworkSecurityGroupID); err != nil {
			return fmt.Errorf("invalid UUID for networkSecurityGroupId: %s", spec.NetworkSecurityGroupID)
		}
	}

	for i, sub := range spec.AdditionalSubnetIDs {
		if _, err := uuid.Parse(sub.SubnetID); err != nil {
			return fmt.Errorf("invalid UUID for additionalSubnetIds[%d].subnetId: %s", i, sub.SubnetID)
		}
	}

	for i, svc := range spec.DpuExtensionServices {
		if svc.ServiceID == "" {
			return fmt.Errorf("dpuExtensionServices[%d].serviceId is required", i)
		}
		if _, err := uuid.Parse(svc.ServiceID); err != nil {
			return fmt.Errorf("invalid UUID for dpuExtensionServices[%d].serviceId: %s", i, svc.ServiceID)
		}
	}

	return nil
}

func validateImmutableFields(oldSpec, newSpec *NicoMachineProviderSpec) error {
	if oldSpec.SiteID != newSpec.SiteID {
		return fmt.Errorf("siteId is immutable (was %q, got %q)", oldSpec.SiteID, newSpec.SiteID)
	}
	if oldSpec.TenantID != newSpec.TenantID {
		return fmt.Errorf("tenantId is immutable (was %q, got %q)", oldSpec.TenantID, newSpec.TenantID)
	}
	return nil
}
