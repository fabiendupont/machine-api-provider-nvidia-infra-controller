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
	"testing"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func validSpec() NicoMachineProviderSpec {
	return NicoMachineProviderSpec{
		SiteID:         "550e8400-e29b-41d4-a716-446655440000",
		TenantID:       "660e8400-e29b-41d4-a716-446655440001",
		InstanceTypeID: "990e8400-e29b-41d4-a716-446655440004",
		VpcID:          "770e8400-e29b-41d4-a716-446655440002",
		SubnetID:       "880e8400-e29b-41d4-a716-446655440003",
	}
}

func TestValidateProviderSpec(t *testing.T) {
	tests := []struct {
		name    string
		spec    NicoMachineProviderSpec
		wantErr bool
	}{
		{
			name:    "valid with instanceTypeId",
			spec:    validSpec(),
			wantErr: false,
		},
		{
			name: "valid with machineId",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.InstanceTypeID = ""
				s.MachineID = "aa0e8400-e29b-41d4-a716-446655440005"
				return s
			}(),
			wantErr: false,
		},
		{
			name: "both instanceTypeId and machineId",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.MachineID = "aa0e8400-e29b-41d4-a716-446655440005"
				return s
			}(),
			wantErr: true,
		},
		{
			name: "neither instanceTypeId nor machineId",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.InstanceTypeID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing siteId",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.SiteID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing tenantId",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.TenantID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing vpcId",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.VpcID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing subnetId",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.SubnetID = ""
				return s
			}(),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProviderSpec(&tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateProviderSpec() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateUUIDs(t *testing.T) {
	tests := []struct {
		name    string
		spec    NicoMachineProviderSpec
		wantErr bool
	}{
		{
			name:    "valid UUIDs",
			spec:    validSpec(),
			wantErr: false,
		},
		{
			name: "invalid siteId UUID",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.SiteID = "not-a-uuid"
				return s
			}(),
			wantErr: true,
		},
		{
			name: "invalid optional operatingSystemId UUID",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.OperatingSystemID = "bad-uuid"
				return s
			}(),
			wantErr: true,
		},
		{
			name: "invalid machineId UUID",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.InstanceTypeID = ""
				s.MachineID = "bad-uuid"
				return s
			}(),
			wantErr: true,
		},
		{
			name: "invalid networkSecurityGroupId UUID",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.NetworkSecurityGroupID = "bad-uuid"
				return s
			}(),
			wantErr: true,
		},
		{
			name: "invalid additionalSubnet UUID",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.AdditionalSubnetIDs = []AdditionalSubnet{{SubnetID: "bad-uuid"}}
				return s
			}(),
			wantErr: true,
		},
		{
			name: "valid additionalSubnet UUID",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.AdditionalSubnetIDs = []AdditionalSubnet{{SubnetID: "aa0e8400-e29b-41d4-a716-446655440005"}}
				return s
			}(),
			wantErr: false,
		},
		{
			name: "dpuExtensionService missing serviceId",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.DpuExtensionServices = []DpuExtensionServiceSpec{{ServiceID: ""}}
				return s
			}(),
			wantErr: true,
		},
		{
			name: "dpuExtensionService invalid UUID",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.DpuExtensionServices = []DpuExtensionServiceSpec{{ServiceID: "bad-uuid"}}
				return s
			}(),
			wantErr: true,
		},
		{
			name: "dpuExtensionService valid UUID",
			spec: func() NicoMachineProviderSpec {
				s := validSpec()
				s.DpuExtensionServices = []DpuExtensionServiceSpec{{ServiceID: "bb0e8400-e29b-41d4-a716-446655440006"}}
				return s
			}(),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUUIDs(&tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateUUIDs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateImmutableFields(t *testing.T) {
	oldSpec := &NicoMachineProviderSpec{
		SiteID:   "550e8400-e29b-41d4-a716-446655440000",
		TenantID: "660e8400-e29b-41d4-a716-446655440001",
	}

	t.Run("same values allowed", func(t *testing.T) {
		newSpec := &NicoMachineProviderSpec{
			SiteID:   "550e8400-e29b-41d4-a716-446655440000",
			TenantID: "660e8400-e29b-41d4-a716-446655440001",
		}
		if err := validateImmutableFields(oldSpec, newSpec); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("changed siteId rejected", func(t *testing.T) {
		newSpec := &NicoMachineProviderSpec{
			SiteID:   "different-site-id",
			TenantID: "660e8400-e29b-41d4-a716-446655440001",
		}
		if err := validateImmutableFields(oldSpec, newSpec); err == nil {
			t.Error("expected error for changed siteId")
		}
	})

	t.Run("changed tenantId rejected", func(t *testing.T) {
		newSpec := &NicoMachineProviderSpec{
			SiteID:   "550e8400-e29b-41d4-a716-446655440000",
			TenantID: "different-tenant-id",
		}
		if err := validateImmutableFields(oldSpec, newSpec); err == nil {
			t.Error("expected error for changed tenantId")
		}
	})
}

// Handle webhook tests

func machineAdmissionRequest(spec NicoMachineProviderSpec) admission.Request {
	specBytes, _ := json.Marshal(spec)
	machine := &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: "test-machine", Namespace: "default"},
		Spec: machinev1beta1.MachineSpec{
			ProviderSpec: machinev1beta1.ProviderSpec{
				Value: &runtime.RawExtension{Raw: specBytes},
			},
		},
	}
	machineBytes, _ := json.Marshal(machine)
	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Object: runtime.RawExtension{Raw: machineBytes},
		},
	}
}

func TestHandle_ValidSpec(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = machinev1beta1.AddToScheme(scheme)
	decoder := admission.NewDecoder(scheme)

	v := &MachineValidator{Decoder: decoder}
	resp := v.Handle(context.Background(), machineAdmissionRequest(validSpec()))
	if !resp.Allowed {
		t.Errorf("Expected allowed, got denied: %s", resp.Result.Message)
	}
}

func TestHandle_InvalidSpec(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = machinev1beta1.AddToScheme(scheme)
	decoder := admission.NewDecoder(scheme)

	spec := validSpec()
	spec.SiteID = ""
	v := &MachineValidator{Decoder: decoder}
	resp := v.Handle(context.Background(), machineAdmissionRequest(spec))
	if resp.Allowed {
		t.Error("Expected denied for missing siteId")
	}
}

func TestHandle_InvalidUUID(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = machinev1beta1.AddToScheme(scheme)
	decoder := admission.NewDecoder(scheme)

	spec := validSpec()
	spec.SiteID = "not-a-uuid"
	v := &MachineValidator{Decoder: decoder}
	resp := v.Handle(context.Background(), machineAdmissionRequest(spec))
	if resp.Allowed {
		t.Error("Expected denied for invalid UUID")
	}
}

func TestHandle_NoProviderSpec(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = machinev1beta1.AddToScheme(scheme)
	decoder := admission.NewDecoder(scheme)

	machine := &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       machinev1beta1.MachineSpec{},
	}
	machineBytes, _ := json.Marshal(machine)
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Object: runtime.RawExtension{Raw: machineBytes},
		},
	}

	v := &MachineValidator{Decoder: decoder}
	resp := v.Handle(context.Background(), req)
	if !resp.Allowed {
		t.Errorf("Expected allowed for no providerSpec, got: %s", resp.Result.Message)
	}
}

func TestHandle_NotNicoSpec(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = machinev1beta1.AddToScheme(scheme)
	decoder := admission.NewDecoder(scheme)

	spec := NicoMachineProviderSpec{}
	v := &MachineValidator{Decoder: decoder}
	resp := v.Handle(context.Background(), machineAdmissionRequest(spec))
	if !resp.Allowed {
		t.Errorf("Expected allowed for non-NICo spec, got: %s", resp.Result.Message)
	}
}

func TestHandle_InvalidJSON(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = machinev1beta1.AddToScheme(scheme)
	decoder := admission.NewDecoder(scheme)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Object: runtime.RawExtension{Raw: []byte(`not-json`)},
		},
	}

	v := &MachineValidator{Decoder: decoder}
	resp := v.Handle(context.Background(), req)
	if resp.Allowed {
		t.Error("Expected error for invalid JSON")
	}
}

func TestHandle_UpdateImmutabilityCheck(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = machinev1beta1.AddToScheme(scheme)
	decoder := admission.NewDecoder(scheme)

	oldSpec := validSpec()
	newSpec := validSpec()
	newSpec.SiteID = "aa0e8400-e29b-41d4-a716-446655440099"

	oldSpecBytes, _ := json.Marshal(oldSpec)
	oldMachine := &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: machinev1beta1.MachineSpec{
			ProviderSpec: machinev1beta1.ProviderSpec{
				Value: &runtime.RawExtension{Raw: oldSpecBytes},
			},
		},
	}
	oldMachineBytes, _ := json.Marshal(oldMachine)

	req := machineAdmissionRequest(newSpec)
	req.OldObject = runtime.RawExtension{Raw: oldMachineBytes}

	v := &MachineValidator{Decoder: decoder}
	resp := v.Handle(context.Background(), req)
	if resp.Allowed {
		t.Error("Expected denied for immutable siteId change on update")
	}
}

func TestHandle_UpdateSameValues(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = machinev1beta1.AddToScheme(scheme)
	decoder := admission.NewDecoder(scheme)

	spec := validSpec()
	specBytes, _ := json.Marshal(spec)
	machine := &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: machinev1beta1.MachineSpec{
			ProviderSpec: machinev1beta1.ProviderSpec{
				Value: &runtime.RawExtension{Raw: specBytes},
			},
		},
	}
	machineBytes, _ := json.Marshal(machine)

	req := machineAdmissionRequest(spec)
	req.OldObject = runtime.RawExtension{Raw: machineBytes}

	v := &MachineValidator{Decoder: decoder}
	resp := v.Handle(context.Background(), req)
	if !resp.Allowed {
		t.Errorf("Expected allowed for same values on update, got: %s", resp.Result.Message)
	}
}
