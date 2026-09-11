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

package machine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	nico "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
	v1beta1 "github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/apis/nicoprovider/v1beta1"
	"github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/providerid"
)

const (
	testInstanceID      = "test-instance-id"
	testMachineID       = "machine-123"
	testTargetMachineID = "target-machine-id"
	testMachineName     = "test-machine"
	testNamespace       = "default"
	testCredsSecret     = "nico-creds"
	testAlertID         = "alert-1"
	testSiteID          = "550e8400-e29b-41d4-a716-446655440000"
	testHealthSource    = "hardware-monitor"
	testReportMode      = "replace"
	testValidationName  = "hw-validation"
	testValidationCtx   = "pre-provision"
	testValidationDone  = "completed"
	testBuildSiteID     = "site-1"
	testBuildTenantID   = "tenant-1"
	testBuildVpcID      = "vpc-1"
	testBuildSubnetID   = "subnet-1"
	mhcAnnotation       = "machine.openshift.io/unhealthy"
	secretKeyOrgName    = "orgName"
)

// mockNicoClient implements NicoClientInterface for testing
type mockNicoClient struct {
	createInstance func(
		ctx context.Context, org string, req nico.InstanceCreateRequest,
	) (*nico.Instance, *http.Response, error)
	getInstance func(
		ctx context.Context, org string, instanceId string,
	) (*nico.Instance, *http.Response, error)
	deleteInstance func(
		ctx context.Context, org string, instanceId string,
		deleteReq *nico.InstanceDeleteRequest,
	) (*http.Response, error)
	getMachine func(
		ctx context.Context, org string, machineId string,
	) (*nico.Machine, *http.Response, error)
	updateInstance func(
		ctx context.Context, org string, instanceId string,
		req nico.InstanceUpdateRequest,
	) (*nico.Instance, *http.Response, error)
	getInstanceStatusHistory func(
		ctx context.Context, org string, instanceId string,
	) ([]nico.StatusDetail, *http.Response, error)
	getAllMachineHealthReport func(
		ctx context.Context, org string, machineId string,
	) ([]nico.MachineHealthReportEntry, *http.Response, error)
	createOrUpdateMachineHealthReport func(
		ctx context.Context, org string, machineId string,
		req nico.MachineHealthReportEntryRequest,
	) (*nico.MachineHealthReportEntry, *http.Response, error)
	deleteMachineHealthReport func(
		ctx context.Context, org string, machineId string, source string,
	) (*http.Response, error)
	getMachineValidationRuns func(
		ctx context.Context, org string, machineId string,
	) ([]nico.MachineValidationRun, *http.Response, error)
	getMachineStatusHistory func(
		ctx context.Context, org string, machineId string,
	) ([]nico.StatusDetail, *http.Response, error)
	getAllTenantAccount func(
		ctx context.Context, org string,
	) ([]nico.TenantAccount, *http.Response, error)
	getCurrentTenant func(
		ctx context.Context, org string,
	) (*nico.Tenant, *http.Response, error)
}

func (m *mockNicoClient) CreateInstance(
	ctx context.Context, org string, req nico.InstanceCreateRequest,
) (*nico.Instance, *http.Response, error) {
	return m.createInstance(ctx, org, req)
}

func (m *mockNicoClient) GetInstance(
	ctx context.Context, org string, instanceId string,
) (*nico.Instance, *http.Response, error) {
	return m.getInstance(ctx, org, instanceId)
}

func (m *mockNicoClient) DeleteInstance(
	ctx context.Context, org string, instanceId string,
	deleteReq *nico.InstanceDeleteRequest,
) (*http.Response, error) {
	return m.deleteInstance(ctx, org, instanceId, deleteReq)
}

func (m *mockNicoClient) GetMachine(
	ctx context.Context, org string, machineId string,
) (*nico.Machine, *http.Response, error) {
	if m.getMachine != nil {
		return m.getMachine(ctx, org, machineId)
	}
	return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func (m *mockNicoClient) GetCurrentTenant(
	ctx context.Context, org string,
) (*nico.Tenant, *http.Response, error) {
	if m.getCurrentTenant != nil {
		return m.getCurrentTenant(ctx, org)
	}
	return nil, &http.Response{StatusCode: 200}, nil
}

func (m *mockNicoClient) GetInstanceStatusHistory(
	ctx context.Context, org string, instanceId string,
) ([]nico.StatusDetail, *http.Response, error) {
	if m.getInstanceStatusHistory != nil {
		return m.getInstanceStatusHistory(ctx, org, instanceId)
	}
	return nil, &http.Response{StatusCode: 200}, nil
}

func (m *mockNicoClient) UpdateInstance(
	ctx context.Context, org string, instanceId string,
	req nico.InstanceUpdateRequest,
) (*nico.Instance, *http.Response, error) {
	if m.updateInstance != nil {
		return m.updateInstance(ctx, org, instanceId, req)
	}
	return nil, &http.Response{StatusCode: 200}, nil
}

func (m *mockNicoClient) GetAllMachineHealthReport(
	ctx context.Context, org string, machineId string,
) ([]nico.MachineHealthReportEntry, *http.Response, error) {
	if m.getAllMachineHealthReport != nil {
		return m.getAllMachineHealthReport(ctx, org, machineId)
	}
	// Default: API unavailable (triggers JSONB fallback)
	return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func (m *mockNicoClient) CreateOrUpdateMachineHealthReport(
	ctx context.Context, org string, machineId string,
	req nico.MachineHealthReportEntryRequest,
) (*nico.MachineHealthReportEntry, *http.Response, error) {
	if m.createOrUpdateMachineHealthReport != nil {
		return m.createOrUpdateMachineHealthReport(ctx, org, machineId, req)
	}
	return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func (m *mockNicoClient) DeleteMachineHealthReport(
	ctx context.Context, org string, machineId string, source string,
) (*http.Response, error) {
	if m.deleteMachineHealthReport != nil {
		return m.deleteMachineHealthReport(ctx, org, machineId, source)
	}
	return &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func (m *mockNicoClient) GetMachineValidationRuns(
	ctx context.Context, org string, machineId string,
) ([]nico.MachineValidationRun, *http.Response, error) {
	if m.getMachineValidationRuns != nil {
		return m.getMachineValidationRuns(ctx, org, machineId)
	}
	return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func (m *mockNicoClient) GetMachineStatusHistory(
	ctx context.Context, org string, machineId string,
) ([]nico.StatusDetail, *http.Response, error) {
	if m.getMachineStatusHistory != nil {
		return m.getMachineStatusHistory(ctx, org, machineId)
	}
	return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func (m *mockNicoClient) GetAllTenantAccount(
	ctx context.Context, org string,
) ([]nico.TenantAccount, *http.Response, error) {
	if m.getAllTenantAccount != nil {
		return m.getAllTenantAccount(ctx, org)
	}
	return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func (m *mockNicoClient) GetAllMachine(
	ctx context.Context, org string,
) ([]nico.Machine, *http.Response, error) {
	return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func (m *mockNicoClient) GetAllSku(
	ctx context.Context, org string,
) ([]nico.Sku, *http.Response, error) {
	return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func (m *mockNicoClient) GetAllSiteExplorerEndpoint(
	ctx context.Context, org string,
) ([]nico.ExploredEndpoint, *http.Response, error) {
	return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func newTestActuator(mock *mockNicoClient) *Actuator {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = machinev1beta1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	recorder := events.NewFakeRecorder(10)
	return NewActuatorWithClient(fakeClient, recorder, mock, "test-org")
}

func newTestActuatorWithMachine(
	mock *mockNicoClient, machine *machinev1beta1.Machine,
) (*Actuator, *events.FakeRecorder) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = machinev1beta1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(machine).
		WithStatusSubresource(machine).
		Build()

	recorder := events.NewFakeRecorder(10)
	actuator := NewActuatorWithClient(fakeClient, recorder, mock, "test-org")
	return actuator, recorder
}

func testInstance(id string) *nico.Instance {
	status := nico.INSTANCESTATUS_PROVISIONING
	machineId := nico.NewNullableString(ptr(testMachineID))
	return &nico.Instance{
		Id:        &id,
		Status:    &status,
		MachineId: *machineId,
		Interfaces: []nico.Interface{
			{
				IpAddresses: []string{"10.0.0.1"},
			},
		},
	}
}

func validProviderSpec() v1beta1.NicoMachineProviderSpec {
	return v1beta1.NicoMachineProviderSpec{
		SiteID:         testSiteID,
		TenantID:       "660e8400-e29b-41d4-a716-446655440001",
		InstanceTypeID: "990e8400-e29b-41d4-a716-446655440004",
		VpcID:          "770e8400-e29b-41d4-a716-446655440002",
		SubnetID:       "880e8400-e29b-41d4-a716-446655440003",
		CredentialsSecret: v1beta1.CredentialsSecretReference{
			Name:      testCredsSecret,
			Namespace: testNamespace,
		},
	}
}

func createTypedTestMachine(providerSpec v1beta1.NicoMachineProviderSpec) *machinev1beta1.Machine {
	specBytes, _ := json.Marshal(providerSpec)
	return &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testMachineName,
			Namespace: testNamespace,
		},
		Spec: machinev1beta1.MachineSpec{
			ProviderSpec: machinev1beta1.ProviderSpec{
				Value: &runtime.RawExtension{Raw: specBytes},
			},
		},
	}
}

func createTestMachine(providerSpec v1beta1.NicoMachineProviderSpec) *unstructured.Unstructured {
	machine := &unstructured.Unstructured{}
	machine.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "machine.openshift.io",
		Version: "v1beta1",
		Kind:    "Machine",
	})
	machine.SetName(testMachineName)
	machine.SetNamespace(testNamespace)

	providerSpecMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&providerSpec)
	_ = unstructured.SetNestedField(machine.Object, providerSpecMap, "spec", "providerSpec", "value")

	return machine
}

func createTestMachineWithStatus(
	providerSpec v1beta1.NicoMachineProviderSpec,
	providerStatus v1beta1.NicoMachineProviderStatus,
) *unstructured.Unstructured {
	machine := createTestMachine(providerSpec)

	statusMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&providerStatus)
	_ = unstructured.SetNestedField(machine.Object, statusMap, "status", "providerStatus")

	return machine
}

func TestCreate_Success(t *testing.T) {
	instanceID := uuid.New().String()
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
	}

	machine := createTypedTestMachine(validProviderSpec())
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
}

func TestCreate_APIError(t *testing.T) {
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return nil, nil, fmt.Errorf("connection refused")
		},
	}

	actuator := newTestActuator(mock)
	machine := createTestMachine(validProviderSpec())

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error, got nil")
	}
}

func TestCreate_InvalidSpec(t *testing.T) {
	createCalled := false
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			createCalled = true
			return nil, nil, nil
		},
	}

	actuator := newTestActuator(mock)

	// Both instanceTypeId and machineId set
	spec := validProviderSpec()
	spec.MachineID = "some-machine-id"
	machine := createTestMachine(spec)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected validation error, got nil")
	}
	if createCalled {
		t.Error("Create() should not have called the API with invalid spec")
	}
}

func TestCreate_MissingRequiredFields(t *testing.T) {
	mock := &mockNicoClient{}
	actuator := newTestActuator(mock)

	// Neither instanceTypeId nor machineId
	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	machine := createTestMachine(spec)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected validation error for missing instanceTypeId/machineId")
	}
}

func TestExists_TransientError(t *testing.T) {
	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, instanceId string) (*nico.Instance, *http.Response, error) {
			return nil, nil, fmt.Errorf("connection timeout")
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})

	exists, err := actuator.Exists(context.Background(), machine)
	if err == nil {
		t.Fatal("Exists() expected error on transient failure, got nil")
	}
	if exists {
		t.Error("Exists() should return false on error")
	}
}

func TestExists_NotFound(t *testing.T) {
	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, instanceId string) (*nico.Instance, *http.Response, error) {
			return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})

	exists, err := actuator.Exists(context.Background(), machine)
	if err != nil {
		t.Fatalf("Exists() unexpected error for 404: %v", err)
	}
	if exists {
		t.Error("Exists() should return false for 404")
	}
}

func TestExists_InstanceExists(t *testing.T) {
	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, instanceId string) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceId), &http.Response{StatusCode: 200}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})

	exists, err := actuator.Exists(context.Background(), machine)
	if err != nil {
		t.Fatalf("Exists() unexpected error: %v", err)
	}
	if !exists {
		t.Error("Exists() should return true when instance exists")
	}
}

func TestExists_NoInstanceID(t *testing.T) {
	mock := &mockNicoClient{}
	actuator := newTestActuator(mock)
	machine := createTestMachine(validProviderSpec())

	exists, err := actuator.Exists(context.Background(), machine)
	if err != nil {
		t.Fatalf("Exists() unexpected error: %v", err)
	}
	if exists {
		t.Error("Exists() should return false when no instance ID is set")
	}
}

func TestDelete_AlreadyDeleted(t *testing.T) {
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			return &http.Response{StatusCode: 404}, fmt.Errorf("not found")
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error for already-deleted instance: %v", err)
	}
}

func TestDelete_Success(t *testing.T) {
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			return &http.Response{StatusCode: 200}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
}

func TestDelete_NoInstanceID(t *testing.T) {
	mock := &mockNicoClient{}
	actuator := newTestActuator(mock)
	machine := createTestMachine(validProviderSpec())

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error when no instance ID: %v", err)
	}
}

func TestValidateProviderSpec(t *testing.T) {
	tests := []struct {
		name    string
		spec    v1beta1.NicoMachineProviderSpec
		wantErr bool
	}{
		{
			name:    "valid with instanceTypeId",
			spec:    validProviderSpec(),
			wantErr: false,
		},
		{
			name: "valid with machineId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
				s.InstanceTypeID = ""
				s.MachineID = "machine-id"
				return s
			}(),
			wantErr: false,
		},
		{
			name: "both instanceTypeId and machineId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
				s.MachineID = "machine-id"
				return s
			}(),
			wantErr: true,
		},
		{
			name: "neither instanceTypeId nor machineId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
				s.InstanceTypeID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing siteId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
				s.SiteID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing tenantId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
				s.TenantID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing vpcId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
				s.VpcID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing subnetId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
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

func TestUpdate_StateTracking(t *testing.T) {
	instanceID := uuid.New().String()

	tests := []struct {
		name           string
		status         nico.InstanceStatus
		expectReady    bool
		expectCondType string
	}{
		{"Pending", nico.INSTANCESTATUS_PENDING, false, "InstanceAllocating"},
		{"Provisioning", nico.INSTANCESTATUS_PROVISIONING, false, "InstanceProvisioning"},
		{"Configuring", nico.INSTANCESTATUS_CONFIGURING, false, "InstanceBootstrapping"},
		{"Ready", nico.INSTANCESTATUS_READY, true, "InstanceReady"},
		{"Terminating", nico.INSTANCESTATUS_TERMINATING, false, "InstanceTerminating"},
		{"Error", nico.INSTANCESTATUS_ERROR, false, "InstanceError"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockNicoClient{
				getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
					inst := testInstance(instanceID)
					inst.Status = tt.status.Ptr()
					return inst, &http.Response{StatusCode: 200}, nil
				},
			}

			providerStatus := v1beta1.NicoMachineProviderStatus{
				InstanceID: &instanceID,
			}
			machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
			actuator, _ := newTestActuatorWithMachine(mock, machine)

			err := actuator.Update(context.Background(), machine)
			if err != nil {
				t.Fatalf("Update() unexpected error: %v", err)
			}
		})
	}
}

func TestUpdate_HealthIntegration(t *testing.T) {
	instanceID := uuid.New().String()
	machineID := testMachineID

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &machineID,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{Id: testAlertID, Message: "unknown issue"},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestUpdate_HealthyMachine(t *testing.T) {
	instanceID := uuid.New().String()
	machineID := testMachineID

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &machineID,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func createTypedTestMachineWithStatus(
	providerSpec v1beta1.NicoMachineProviderSpec,
	providerStatus v1beta1.NicoMachineProviderStatus,
) *machinev1beta1.Machine {
	specBytes, _ := json.Marshal(providerSpec)
	statusBytes, _ := json.Marshal(providerStatus)
	return &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testMachineName,
			Namespace: testNamespace,
		},
		Spec: machinev1beta1.MachineSpec{
			ProviderSpec: machinev1beta1.ProviderSpec{
				Value: &runtime.RawExtension{Raw: specBytes},
			},
		},
		Status: machinev1beta1.MachineStatus{
			ProviderStatus: &runtime.RawExtension{Raw: statusBytes},
		},
	}
}

func TestDelete_MHCRemediation(t *testing.T) {
	var capturedDeleteReq *nico.InstanceDeleteRequest
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			capturedDeleteReq = deleteReq
			return &http.Response{StatusCode: 200}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})
	// Set the MHC remediation annotation
	machine.SetAnnotations(map[string]string{
		mhcAnnotation: "",
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if capturedDeleteReq == nil {
		t.Fatal("Delete() should have passed InstanceDeleteRequest for MHC remediation")
	}
	if capturedDeleteReq.MachineHealthIssue == nil {
		t.Fatal("Delete() should have set MachineHealthIssue")
	}
	if capturedDeleteReq.MachineHealthIssue.Category != "MachineHealthCheck" {
		t.Errorf("Expected category MachineHealthCheck, got %s", capturedDeleteReq.MachineHealthIssue.Category)
	}
}

func TestDelete_NoMHCRemediation(t *testing.T) {
	var capturedDeleteReq *nico.InstanceDeleteRequest
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			capturedDeleteReq = deleteReq
			return &http.Response{StatusCode: 200}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if capturedDeleteReq != nil {
		t.Error("Delete() should not have passed InstanceDeleteRequest without MHC annotation")
	}
}

func TestUpdate_StatusHistoryOnError(t *testing.T) {
	instanceID := uuid.New().String()
	errorStatus := nico.INSTANCESTATUS_ERROR

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			inst.Status = &errorStatus
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getInstanceStatusHistory: func(
			ctx context.Context, org string, id string,
		) ([]nico.StatusDetail, *http.Response, error) {
			now := time.Now()
			errorMsg := "Machine allocation failed"
			errorStr := string(nico.INSTANCESTATUS_ERROR)
			provStr := string(nico.INSTANCESTATUS_PROVISIONING)
			return []nico.StatusDetail{
				{Status: &provStr, Created: &now},
				{Status: &errorStr, Message: *nico.NewNullableString(&errorMsg), Created: &now},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, recorder := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	// Verify Warning events were recorded
	close(recorder.Events)
	eventCount := 0
	for range recorder.Events {
		eventCount++
	}
	if eventCount == 0 {
		t.Error("Expected Warning events for status history on Error state")
	}
}

func TestCreate_WithDpuExtensionServices(t *testing.T) {
	instanceID := uuid.New().String()
	updateCalled := false
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		updateInstance: func(
			ctx context.Context, org string, id string,
			req nico.InstanceUpdateRequest,
		) (*nico.Instance, *http.Response, error) {
			updateCalled = true
			if len(req.DpuExtensionServiceDeployments) != 1 {
				t.Errorf("Expected 1 DPU deployment, got %d", len(req.DpuExtensionServiceDeployments))
			}
			return testInstance(instanceID), &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.DpuExtensionServices = []v1beta1.DpuExtensionServiceSpec{
		{ServiceID: "aa0e8400-e29b-41d4-a716-446655440010", Version: "1.0.0"},
	}
	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if !updateCalled {
		t.Error("Create() should have called UpdateInstance for DPU extension services")
	}
}

func TestProviderIDParsing(t *testing.T) {
	pid := providerid.NewProviderID("test-org", "test-tenant", "test-site", uuid.New())

	parsed, err := providerid.ParseProviderID(pid.String())
	if err != nil {
		t.Fatalf("Failed to parse provider ID: %v", err)
	}

	if parsed.OrgName != "test-org" {
		t.Errorf("Expected orgName=test-org, got %s", parsed.OrgName)
	}
	if parsed.TenantName != "test-tenant" {
		t.Errorf("Expected tenantName=test-tenant, got %s", parsed.TenantName)
	}
	if parsed.SiteName != "test-site" {
		t.Errorf("Expected siteName=test-site, got %s", parsed.SiteName)
	}
}

func TestCreate_HTTPStatusErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantKind   APIErrorKind
	}{
		{"400 terminal", 400, ErrorTerminal},
		{"429 transient", 429, ErrorTransient},
		{"500 transient", 500, ErrorTransient},
		{"503 transient", 503, ErrorTransient},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockNicoClient{
				createInstance: func(
					ctx context.Context, org string, req nico.InstanceCreateRequest,
				) (*nico.Instance, *http.Response, error) {
					return nil, &http.Response{
						StatusCode: tt.statusCode,
						Body:       io.NopCloser(strings.NewReader(`{"message":"test error"}`)),
					}, fmt.Errorf("API error %d", tt.statusCode)
				},
			}

			machine := createTypedTestMachine(validProviderSpec())
			actuator, _ := newTestActuatorWithMachine(mock, machine)

			err := actuator.Create(context.Background(), machine)
			if err == nil {
				t.Fatal("Create() expected error, got nil")
			}

			var classified *ClassifiedError
			if !errors.As(err, &classified) {
				t.Fatalf("expected ClassifiedError, got %T: %v", err, err)
			}
			if classified.Kind != tt.wantKind {
				t.Errorf("error kind = %d, want %d", classified.Kind, tt.wantKind)
			}
		})
	}
}

func TestDelete_APIError500(t *testing.T) {
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			return &http.Response{StatusCode: 500}, fmt.Errorf("internal server error")
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})

	err := actuator.Delete(context.Background(), machine)
	if err == nil {
		t.Fatal("Delete() expected error for 500, got nil")
	}
}

func TestDelete_HTTP202Accepted(t *testing.T) {
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			return &http.Response{StatusCode: 202}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error for 202: %v", err)
	}
}

func TestCreate_NilInstance(t *testing.T) {
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return nil, &http.Response{StatusCode: 201}, nil
		},
	}

	machine := createTypedTestMachine(validProviderSpec())
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error for nil instance, got nil")
	}
}

func TestUpdate_ProvisioningTimeout(t *testing.T) {
	// Temporarily reduce timeout for testing
	origTimeout := ProvisioningTimeout
	ProvisioningTimeout = 1 * time.Millisecond
	defer func() { ProvisioningTimeout = origTimeout }()

	instanceID := uuid.New().String()
	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			status := nico.INSTANCESTATUS_PROVISIONING
			inst.Status = &status
			return inst, &http.Response{StatusCode: 200}, nil
		},
	}

	// Set InstanceProvisioned condition with a timestamp in the past
	pastTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	providerStatus := v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
		Conditions: []metav1.Condition{
			{
				Type:               "InstanceProvisioned",
				Status:             metav1.ConditionTrue,
				Reason:             "InstanceCreated",
				Message:            "Instance created",
				LastTransitionTime: pastTime,
			},
		},
	}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, recorder := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	// Check for ProvisioningTimeout event
	close(recorder.Events)
	foundTimeout := false
	for event := range recorder.Events {
		if strings.Contains(event, "ProvisioningTimeout") {
			foundTimeout = true
		}
	}
	if !foundTimeout {
		t.Error("Expected ProvisioningTimeout event")
	}

	// Verify ErrorReason is set on the Machine
	if machine.Status.ErrorReason == nil {
		t.Error("Expected ErrorReason to be set")
	}
}

func TestClassifyAlerts(t *testing.T) {
	tests := []struct {
		name         string
		alerts       []nico.MachineHealthProbeAlert
		wantCritical int
		wantWarning  int
	}{
		{
			name:         "no alerts",
			alerts:       nil,
			wantCritical: 0,
			wantWarning:  0,
		},
		{
			name: "critical classification",
			alerts: []nico.MachineHealthProbeAlert{
				{Id: "a1", Message: "err", Classifications: []string{severityCritical}},
			},
			wantCritical: 1,
			wantWarning:  0,
		},
		{
			name: "warning classification",
			alerts: []nico.MachineHealthProbeAlert{
				{Id: "a1", Message: "warn", Classifications: []string{severityWarning}},
			},
			wantCritical: 0,
			wantWarning:  1,
		},
		{
			name: "no classification defaults to critical",
			alerts: []nico.MachineHealthProbeAlert{
				{Id: "a1", Message: "unknown", Classifications: nil},
			},
			wantCritical: 1,
			wantWarning:  0,
		},
		{
			name: "mixed classifications",
			alerts: []nico.MachineHealthProbeAlert{
				{Id: "a1", Message: "err", Classifications: []string{severityCritical}},
				{Id: "a2", Message: "warn", Classifications: []string{severityWarning}},
				{Id: "a3", Message: "other", Classifications: []string{"unknown-type"}},
			},
			wantCritical: 2,
			wantWarning:  1,
		},
		{
			name: "alert with both critical and warning is critical",
			alerts: []nico.MachineHealthProbeAlert{
				{Id: "a1", Message: "mixed", Classifications: []string{severityWarning, severityCritical}},
			},
			wantCritical: 1,
			wantWarning:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			critical, warning := classifyAlerts(tt.alerts)
			if len(critical) != tt.wantCritical {
				t.Errorf("critical count = %d, want %d", len(critical), tt.wantCritical)
			}
			if len(warning) != tt.wantWarning {
				t.Errorf("warning count = %d, want %d", len(warning), tt.wantWarning)
			}
		})
	}
}

func TestUpdate_HealthClassification_Critical(t *testing.T) {
	instanceID := uuid.New().String()
	machineID := testMachineID

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &machineID,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{
							Id:              testAlertID,
							Message:         "GPU memory ECC error",
							Classifications: []string{severityCritical},
						},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{InstanceID: &instanceID}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestUpdate_HealthClassification_WarningOnly(t *testing.T) {
	instanceID := uuid.New().String()
	machineID := testMachineID

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &machineID,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{Id: testAlertID, Message: "minor", Classifications: []string{severityWarning}},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{InstanceID: &instanceID}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestUpdate_NicoFaultRemediation(t *testing.T) {
	instanceID := uuid.New().String()
	machineID := testMachineID

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &machineID,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{
							Id:              testAlertID,
							Message:         "GPU reset in progress",
							Classifications: []string{severityCritical, severityRemediating},
						},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{InstanceID: &instanceID}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestDelete_MHCRemediation_EnrichedDetails(t *testing.T) {
	var capturedDeleteReq *nico.InstanceDeleteRequest
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			capturedDeleteReq = deleteReq
			return &http.Response{StatusCode: 200}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})
	machine.SetAnnotations(map[string]string{
		mhcAnnotation: "",
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if capturedDeleteReq == nil || capturedDeleteReq.MachineHealthIssue == nil {
		t.Fatal("Delete() should have set MachineHealthIssue")
	}

	// Verify enriched summary includes machine name
	summaryPtr := capturedDeleteReq.MachineHealthIssue.Summary.Get()
	if summaryPtr == nil {
		t.Fatal("Expected Summary to be set")
	}
	if !strings.Contains(*summaryPtr, testMachineName) {
		t.Errorf("Expected summary to contain machine name, got: %s", *summaryPtr)
	}

	// Verify details contain structured metadata
	details := capturedDeleteReq.MachineHealthIssue.Details.Get()
	if details == nil {
		t.Fatal("Expected Details to be set")
	}
	if !strings.Contains(*details, "machine_name") {
		t.Errorf("Expected details to contain machine_name, got: %s", *details)
	}
	if !strings.Contains(*details, "detected_at") {
		t.Errorf("Expected details to contain detected_at, got: %s", *details)
	}
}

func TestCreate_PreFlightHealthCheck_BlocksCreation(t *testing.T) {
	createCalled := false
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			createCalled = true
			return testInstance(uuid.New().String()), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &mid,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{
							Id:              testAlertID,
							Message:         "GPU memory ECC error",
							Classifications: []string{severityCritical},
						},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, recorder := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error when pre-flight health check fails")
	}
	if createCalled {
		t.Error("Create() should not have called CreateInstance when pre-flight health check fails")
	}
	if !strings.Contains(err.Error(), "critical health faults") {
		t.Errorf("Expected error about critical health faults, got: %v", err)
	}

	// Check for FaultBlockedCreation event
	close(recorder.Events)
	foundEvent := false
	for event := range recorder.Events {
		if strings.Contains(event, "FaultBlockedCreation") {
			foundEvent = true
		}
	}
	if !foundEvent {
		t.Error("Expected FaultBlockedCreation event")
	}
}

func TestCreate_PreFlightHealthCheck_AllowsHealthyMachine(t *testing.T) {
	instanceID := uuid.New().String()
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &mid,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
}

func TestCreate_PreFlightHealthCheck_SkippedWithAllowUnhealthy(t *testing.T) {
	instanceID := uuid.New().String()
	getMachineCalled := false
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			getMachineCalled = true
			return nil, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID
	spec.AllowUnhealthyMachine = true

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if getMachineCalled {
		t.Error("Create() should not have called GetMachine when AllowUnhealthyMachine is set")
	}
}

func TestCreate_PreFlightHealthCheck_SkippedForInstanceType(t *testing.T) {
	instanceID := uuid.New().String()
	getMachineCalled := false
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			getMachineCalled = true
			return nil, &http.Response{StatusCode: 200}, nil
		},
	}

	// Standard spec uses InstanceTypeID, not MachineID
	machine := createTypedTestMachine(validProviderSpec())
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if getMachineCalled {
		t.Error("Create() should not have called GetMachine for instanceTypeId-based provisioning")
	}
}

func TestCreate_PreFlightHealthCheck_FailureReasonAfterMaxAttempts(t *testing.T) {
	origMax := MaxFaultBlockedAttempts
	MaxFaultBlockedAttempts = 2
	defer func() { MaxFaultBlockedAttempts = origMax }()

	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return nil, nil, fmt.Errorf("should not be called")
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &mid,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{
							Id:              testAlertID,
							Message:         "Persistent GPU fault",
							Classifications: []string{severityCritical},
						},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	// First attempt: set FaultBlocked_1 condition
	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error on first attempt")
	}

	// Second attempt: should set FailureReason (MaxFaultBlockedAttempts=2)
	// Re-read the machine to get updated status with FaultBlocked_1 condition
	err = actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error on second attempt")
	}

	if machine.Status.ErrorReason == nil {
		t.Error("Expected ErrorReason to be set after max attempts")
	} else if string(*machine.Status.ErrorReason) != "PreFlightHealthCheckFailed" {
		t.Errorf("Expected ErrorReason=PreFlightHealthCheckFailed, got %s", string(*machine.Status.ErrorReason))
	}
}

func TestUpdate_HealthFromHealthReportAPI(t *testing.T) {
	instanceID := uuid.New().String()

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getAllMachineHealthReport: func(
			ctx context.Context, org, machineId string,
		) ([]nico.MachineHealthReportEntry, *http.Response, error) {
			return []nico.MachineHealthReportEntry{
				{
					Source: testHealthSource,
					Mode:   testReportMode,
					Alerts: []nico.MachineHealthProbeAlert{
						{Id: testAlertID, Message: "GPU ECC uncorrectable error", Classifications: []string{severityCritical}},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{InstanceID: &instanceID}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestUpdate_HealthFromHealthReportAPI_Remediating(t *testing.T) {
	instanceID := uuid.New().String()

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getAllMachineHealthReport: func(
			ctx context.Context, org, machineId string,
		) ([]nico.MachineHealthReportEntry, *http.Response, error) {
			return []nico.MachineHealthReportEntry{
				{
					Source: testHealthSource,
					Mode:   testReportMode,
					Alerts: []nico.MachineHealthProbeAlert{
						{
							Id: testAlertID, Message: "GPU reset in progress",
							Classifications: []string{severityCritical, severityRemediating},
						},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{InstanceID: &instanceID}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestUpdate_HealthFromHealthReportAPI_NoAlerts(t *testing.T) {
	instanceID := uuid.New().String()

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getAllMachineHealthReport: func(
			ctx context.Context, org, machineId string,
		) ([]nico.MachineHealthReportEntry, *http.Response, error) {
			return []nico.MachineHealthReportEntry{}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{InstanceID: &instanceID}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestDelete_MHCRemediation_ReportsHealthIssue(t *testing.T) {
	var capturedReq *nico.MachineHealthReportEntryRequest
	var capturedMachineId string
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			return &http.Response{StatusCode: 200}, nil
		},
		createOrUpdateMachineHealthReport: func(
			ctx context.Context, org string, machineId string,
			req nico.MachineHealthReportEntryRequest,
		) (*nico.MachineHealthReportEntry, *http.Response, error) {
			capturedReq = &req
			capturedMachineId = machineId
			return &nico.MachineHealthReportEntry{Source: req.Source, Mode: req.Mode}, &http.Response{StatusCode: 200}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machineID := "physical-machine-id"
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
		MachineID:  &machineID,
	})
	machine.SetAnnotations(map[string]string{
		mhcAnnotation: "",
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if capturedReq == nil {
		t.Fatal("Delete() should have called CreateOrUpdateMachineHealthReport for MHC remediation")
	}
	if capturedReq.Source != "k8s-mhc" {
		t.Errorf("Expected source=k8s-mhc, got %s", capturedReq.Source)
	}
	if capturedMachineId != machineID {
		t.Errorf("Expected machineId=%s, got %s", machineID, capturedMachineId)
	}
	if len(capturedReq.Alerts) == 0 {
		t.Fatal("Expected at least one alert in health report request")
	}
	if !strings.Contains(capturedReq.Alerts[0].Message, testMachineName) {
		t.Errorf("Expected alert message to contain machine name, got: %s", capturedReq.Alerts[0].Message)
	}
}

func TestCreate_PreFlightHealthCheck_UsesHealthReportAPI(t *testing.T) {
	createCalled := false
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			createCalled = true
			return testInstance(uuid.New().String()), &http.Response{StatusCode: 201}, nil
		},
		getAllMachineHealthReport: func(
			ctx context.Context, org, machineId string,
		) ([]nico.MachineHealthReportEntry, *http.Response, error) {
			return []nico.MachineHealthReportEntry{
				{
					Source: testHealthSource,
					Mode:   testReportMode,
					Alerts: []nico.MachineHealthProbeAlert{
						{Id: testAlertID, Message: "Persistent GPU fault", Classifications: []string{severityCritical}},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error when health report API reports critical alerts")
	}
	if createCalled {
		t.Error("Create() should not have called CreateInstance")
	}
}

func TestCreate_PreFlightHealthCheck_WarningOnlyAllowsCreation(t *testing.T) {
	instanceID := uuid.New().String()
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &mid,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{Id: testAlertID, Message: "minor", Classifications: []string{severityWarning}},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
}

// Feature 1: Health report cleanup on recovery

func TestUpdate_HealthReportCleanedOnRecovery(t *testing.T) {
	instanceID := uuid.New().String()
	var deletedSource string

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getAllMachineHealthReport: func(
			ctx context.Context, org, machineId string,
		) ([]nico.MachineHealthReportEntry, *http.Response, error) {
			return []nico.MachineHealthReportEntry{}, &http.Response{StatusCode: 200}, nil
		},
		deleteMachineHealthReport: func(
			ctx context.Context, org, machineId, source string,
		) (*http.Response, error) {
			deletedSource = source
			return &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
		Conditions: []metav1.Condition{
			{
				Type:   "MachineHealthy",
				Status: metav1.ConditionFalse,
				Reason: "CriticalFault",
			},
		},
	}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
	if deletedSource != "k8s-mhc" {
		t.Errorf("Expected DeleteMachineHealthReport with source=k8s-mhc, got %q", deletedSource)
	}
}

func TestUpdate_HealthReportNotDeletedWhenStayingHealthy(t *testing.T) {
	instanceID := uuid.New().String()
	deleteCalled := false

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getAllMachineHealthReport: func(
			ctx context.Context, org, machineId string,
		) ([]nico.MachineHealthReportEntry, *http.Response, error) {
			return []nico.MachineHealthReportEntry{}, &http.Response{StatusCode: 200}, nil
		},
		deleteMachineHealthReport: func(
			ctx context.Context, org, machineId, source string,
		) (*http.Response, error) {
			deleteCalled = true
			return &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
		Conditions: []metav1.Condition{
			{
				Type:   "MachineHealthy",
				Status: metav1.ConditionTrue,
				Reason: "Healthy",
			},
		},
	}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
	if deleteCalled {
		t.Error("DeleteMachineHealthReport should not be called when machine stays healthy")
	}
}

// Feature 2: Pre-flight machine validation

func TestCreate_PreFlightValidation_BlocksOnFailure(t *testing.T) {
	createCalled := false
	now := time.Now()
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			createCalled = true
			return testInstance(uuid.New().String()), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id:     &mid,
				Health: &nico.MachineHealth{Alerts: []nico.MachineHealthProbeAlert{}},
			}, &http.Response{StatusCode: 200}, nil
		},
		getMachineValidationRuns: func(
			ctx context.Context, org, machineId string,
		) ([]nico.MachineValidationRun, *http.Response, error) {
			endTime := *nico.NewNullableTime(&now)
			return []nico.MachineValidationRun{
				{
					ValidationID: "v1",
					MachineID:    machineId,
					StartTime:    now.Add(-10 * time.Minute),
					EndTime:      endTime,
					Name:         testValidationName,
					Context:      testValidationCtx,
					Status:       nico.MachineValidationStatus{State: testValidationDone, Total: 5, Completed: 3},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error when validation has failures")
	}
	if createCalled {
		t.Error("Create() should not have called CreateInstance")
	}
}

func TestCreate_PreFlightValidation_SkipsOn403(t *testing.T) {
	instanceID := uuid.New().String()
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id:     &mid,
				Health: &nico.MachineHealth{Alerts: []nico.MachineHealthProbeAlert{}},
			}, &http.Response{StatusCode: 200}, nil
		},
		getMachineValidationRuns: func(
			ctx context.Context, org, machineId string,
		) ([]nico.MachineValidationRun, *http.Response, error) {
			return nil, &http.Response{StatusCode: 403}, fmt.Errorf("forbidden")
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
}

func TestCreate_PreFlightValidation_AllowsOnPass(t *testing.T) {
	instanceID := uuid.New().String()
	now := time.Now()
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id:     &mid,
				Health: &nico.MachineHealth{Alerts: []nico.MachineHealthProbeAlert{}},
			}, &http.Response{StatusCode: 200}, nil
		},
		getMachineValidationRuns: func(
			ctx context.Context, org, machineId string,
		) ([]nico.MachineValidationRun, *http.Response, error) {
			endTime := *nico.NewNullableTime(&now)
			return []nico.MachineValidationRun{
				{
					ValidationID: "v1",
					MachineID:    machineId,
					StartTime:    now.Add(-10 * time.Minute),
					EndTime:      endTime,
					Name:         testValidationName,
					Context:      testValidationCtx,
					Status:       nico.MachineValidationStatus{State: testValidationDone, Total: 5, Completed: 5},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
}

// Feature 3: Machine status history on stuck provisioning

func TestUpdate_MachineStatusHistory_EmittedWhenStuck(t *testing.T) {
	instanceID := uuid.New().String()
	machineStatusHistoryCalled := false

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			status := nico.INSTANCESTATUS_PROVISIONING
			inst.Status = &status
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getInstanceStatusHistory: func(
			ctx context.Context, org string, id string,
		) ([]nico.StatusDetail, *http.Response, error) {
			longAgo := time.Now().Add(-10 * time.Minute)
			provStr := string(nico.INSTANCESTATUS_PROVISIONING)
			return []nico.StatusDetail{
				{Status: &provStr, Created: &longAgo},
			}, &http.Response{StatusCode: 200}, nil
		},
		getMachineStatusHistory: func(
			ctx context.Context, org, machineId string,
		) ([]nico.StatusDetail, *http.Response, error) {
			machineStatusHistoryCalled = true
			now := time.Now()
			s1 := "firmware_update"
			return []nico.StatusDetail{
				{Status: &s1, Created: &now},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, recorder := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
	if !machineStatusHistoryCalled {
		t.Error("Expected GetMachineStatusHistory to be called for stuck provisioning")
	}

	close(recorder.Events)
	foundMachineHistory := false
	for event := range recorder.Events {
		if strings.Contains(event, "MachineStatusHistory") {
			foundMachineHistory = true
		}
	}
	if !foundMachineHistory {
		t.Error("Expected MachineStatusHistory event")
	}
}

// Coverage: buildInstanceRequest optional fields

func TestBuildInstanceRequest_AllOptionalFields(t *testing.T) {
	devInstance := int32(2)
	spec := &v1beta1.NicoMachineProviderSpec{
		SiteID:                   testBuildSiteID,
		TenantID:                 testBuildTenantID,
		VpcID:                    testBuildVpcID,
		SubnetID:                 testBuildSubnetID,
		InstanceTypeID:           "itype-1",
		UserData:                 "#!/bin/bash\necho hello",
		OperatingSystemID:        "os-1",
		SSHKeyGroupIDs:           []string{"sshkg-1", "sshkg-2"},
		Labels:                   map[string]string{"env": "test"},
		NetworkSecurityGroupID:   "nsg-1",
		Description:              "test instance",
		AlwaysBootWithCustomIpxe: true,
		AdditionalSubnetIDs: []v1beta1.AdditionalSubnet{
			{SubnetID: "subnet-2", IsPhysical: true},
		},
		InfiniBandInterfaces: []v1beta1.InfiniBandInterfaceSpec{
			{PartitionID: "ib-part-1", Device: "mlx5_0", IsPhysical: true, DeviceInstance: &devInstance},
		},
		NVLinkInterfaces: []v1beta1.NVLinkInterfaceSpec{
			{NVLinkLogicalPartitionID: "nvl-part-1", DeviceInstance: &devInstance},
		},
	}

	req := buildInstanceRequest(testMachineName, spec)

	if req.Name != testMachineName {
		t.Errorf("Name = %s, want test-machine", req.Name)
	}
	if req.UserData.Get() == nil || *req.UserData.Get() != spec.UserData {
		t.Error("UserData not set correctly")
	}
	if req.OperatingSystemId.Get() == nil || *req.OperatingSystemId.Get() != "os-1" {
		t.Error("OperatingSystemId not set correctly")
	}
	if len(req.SshKeyGroupIds) != 2 {
		t.Errorf("SshKeyGroupIds count = %d, want 2", len(req.SshKeyGroupIds))
	}
	if req.Labels["env"] != "test" {
		t.Error("Labels not set correctly")
	}
	if req.NetworkSecurityGroupId.Get() == nil || *req.NetworkSecurityGroupId.Get() != "nsg-1" {
		t.Error("NetworkSecurityGroupId not set correctly")
	}
	if req.Description.Get() == nil || *req.Description.Get() != "test instance" {
		t.Error("Description not set correctly")
	}
	if req.AlwaysBootWithCustomIpxe == nil || !*req.AlwaysBootWithCustomIpxe {
		t.Error("AlwaysBootWithCustomIpxe not set")
	}
	// 1 primary + 1 additional
	if len(req.Interfaces) != 2 {
		t.Errorf("Interfaces count = %d, want 2", len(req.Interfaces))
	}
	if len(req.InfinibandInterfaces) != 1 {
		t.Errorf("InfinibandInterfaces count = %d, want 1", len(req.InfinibandInterfaces))
	}
	ib := req.InfinibandInterfaces[0]
	if ib.PartitionId == nil || *ib.PartitionId != "ib-part-1" {
		t.Error("IB PartitionId not set")
	}
	if ib.Device == nil || *ib.Device != "mlx5_0" {
		t.Error("IB Device not set")
	}
	if ib.IsPhysical == nil || !*ib.IsPhysical {
		t.Error("IB IsPhysical not set")
	}
	if ib.DeviceInstance == nil || *ib.DeviceInstance != 2 {
		t.Error("IB DeviceInstance not set")
	}
	if len(req.NvLinkInterfaces) != 1 {
		t.Errorf("NvLinkInterfaces count = %d, want 1", len(req.NvLinkInterfaces))
	}
	nvl := req.NvLinkInterfaces[0]
	if nvl.NvLinkLogicalPartitionId == nil || *nvl.NvLinkLogicalPartitionId != "nvl-part-1" {
		t.Error("NVLink PartitionId not set")
	}
	if nvl.DeviceInstance == nil || *nvl.DeviceInstance != 2 {
		t.Error("NVLink DeviceInstance not set")
	}
}

func TestBuildInstanceRequest_FallbackIpxeScript(t *testing.T) {
	spec := &v1beta1.NicoMachineProviderSpec{
		SiteID:         testBuildSiteID,
		TenantID:       testBuildTenantID,
		VpcID:          testBuildVpcID,
		SubnetID:       testBuildSubnetID,
		InstanceTypeID: "itype-1",
	}

	req := buildInstanceRequest("test", spec)

	if req.IpxeScript.Get() == nil {
		t.Fatal("IpxeScript should be set when OperatingSystemID is empty")
	}
	if req.OperatingSystemId.Get() != nil {
		t.Error("OperatingSystemId should not be set")
	}
}

func TestBuildInstanceRequest_MachineID(t *testing.T) {
	spec := &v1beta1.NicoMachineProviderSpec{
		SiteID:                testBuildSiteID,
		TenantID:              testBuildTenantID,
		VpcID:                 testBuildVpcID,
		SubnetID:              testBuildSubnetID,
		MachineID:             "machine-1",
		AllowUnhealthyMachine: true,
	}

	req := buildInstanceRequest("test", spec)

	if req.MachineId.Get() == nil || *req.MachineId.Get() != "machine-1" {
		t.Error("MachineId not set correctly")
	}
	if req.AllowUnhealthyMachine == nil || !*req.AllowUnhealthyMachine {
		t.Error("AllowUnhealthyMachine not set")
	}
	if req.InstanceTypeId.Get() != nil {
		t.Error("InstanceTypeId should not be set when MachineID is used")
	}
}

// Coverage: getNicoClient secret validation

func TestGetNicoClient_MissingEndpoint(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = machinev1beta1.AddToScheme(scheme)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testCredsSecret, Namespace: testNamespace},
		Data: map[string][]byte{
			"orgName": []byte("org"),
			"token":   []byte("tok"),
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	actuator := &Actuator{client: fakeClient}

	spec := &v1beta1.NicoMachineProviderSpec{
		CredentialsSecret: v1beta1.CredentialsSecretReference{Name: testCredsSecret, Namespace: testNamespace},
	}
	_, _, err := actuator.getNicoClient(context.Background(), spec)
	if err == nil {
		t.Fatal("Expected error for missing endpoint")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("Expected error about 'endpoint', got: %v", err)
	}
}

func TestGetNicoClient_MissingOrgName(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = machinev1beta1.AddToScheme(scheme)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testCredsSecret, Namespace: testNamespace},
		Data: map[string][]byte{
			"endpoint": []byte("https://api.test"),
			"token":    []byte("tok"),
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	actuator := &Actuator{client: fakeClient}

	spec := &v1beta1.NicoMachineProviderSpec{
		CredentialsSecret: v1beta1.CredentialsSecretReference{Name: testCredsSecret, Namespace: testNamespace},
	}
	_, _, err := actuator.getNicoClient(context.Background(), spec)
	if err == nil {
		t.Fatal("Expected error for missing orgName")
	}
}

func TestGetNicoClient_MissingToken(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = machinev1beta1.AddToScheme(scheme)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testCredsSecret, Namespace: testNamespace},
		Data: map[string][]byte{
			"endpoint": []byte("https://api.test"),
			"orgName":  []byte("org"),
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	actuator := &Actuator{client: fakeClient}

	spec := &v1beta1.NicoMachineProviderSpec{
		CredentialsSecret: v1beta1.CredentialsSecretReference{Name: testCredsSecret, Namespace: testNamespace},
	}
	_, _, err := actuator.getNicoClient(context.Background(), spec)
	if err == nil {
		t.Fatal("Expected error for missing token")
	}
}

func TestGetNicoClient_SecretNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = machinev1beta1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	actuator := &Actuator{client: fakeClient}

	spec := &v1beta1.NicoMachineProviderSpec{
		CredentialsSecret: v1beta1.CredentialsSecretReference{Name: "missing", Namespace: testNamespace},
	}
	_, _, err := actuator.getNicoClient(context.Background(), spec)
	if err == nil {
		t.Fatal("Expected error for missing secret")
	}
}

func TestGetNicoClient_Success(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = machinev1beta1.AddToScheme(scheme)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testCredsSecret, Namespace: testNamespace},
		Data: map[string][]byte{
			"endpoint": []byte("https://api.test"),
			"orgName":  []byte("test-org"),
			"token":    []byte("test-token"),
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	actuator := &Actuator{client: fakeClient}

	spec := &v1beta1.NicoMachineProviderSpec{
		CredentialsSecret: v1beta1.CredentialsSecretReference{Name: testCredsSecret, Namespace: testNamespace},
	}
	client, orgName, err := actuator.getNicoClient(context.Background(), spec)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("Expected non-nil client")
	}
	if orgName != "test-org" {
		t.Errorf("Expected orgName=test-org, got %s", orgName)
	}
}

// Coverage: checkMachineValidation edge cases

func TestCreate_PreFlightValidation_BlocksOnInProgressRun(t *testing.T) {
	createCalled := false
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			createCalled = true
			return testInstance(uuid.New().String()), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id:     &mid,
				Health: &nico.MachineHealth{Alerts: []nico.MachineHealthProbeAlert{}},
			}, &http.Response{StatusCode: 200}, nil
		},
		getMachineValidationRuns: func(
			ctx context.Context, org, machineId string,
		) ([]nico.MachineValidationRun, *http.Response, error) {
			return []nico.MachineValidationRun{
				{
					ValidationID: "v1",
					MachineID:    machineId,
					StartTime:    time.Now().Add(-2 * time.Minute),
					Name:         testValidationName,
					Context:      testValidationCtx,
					Status:       nico.MachineValidationStatus{State: "running", Total: 5, Completed: 2},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error when validation run is in progress")
	}
	if createCalled {
		t.Error("Create() should not have called CreateInstance")
	}
}

func TestCreate_PreFlightValidation_PicksLatestRun(t *testing.T) {
	instanceID := uuid.New().String()
	now := time.Now()
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id:     &mid,
				Health: &nico.MachineHealth{Alerts: []nico.MachineHealthProbeAlert{}},
			}, &http.Response{StatusCode: 200}, nil
		},
		getMachineValidationRuns: func(
			ctx context.Context, org, machineId string,
		) ([]nico.MachineValidationRun, *http.Response, error) {
			oldTime := now.Add(-1 * time.Hour)
			oldEnd := *nico.NewNullableTime(&oldTime)
			newEnd := *nico.NewNullableTime(&now)
			return []nico.MachineValidationRun{
				{
					ValidationID: "v-old",
					MachineID:    machineId,
					StartTime:    oldTime.Add(-10 * time.Minute),
					EndTime:      oldEnd,
					Name:         "old-run",
					Context:      testValidationCtx,
					Status:       nico.MachineValidationStatus{State: testValidationDone, Total: 5, Completed: 3},
				},
				{
					ValidationID: "v-new",
					MachineID:    machineId,
					StartTime:    now.Add(-10 * time.Minute),
					EndTime:      newEnd,
					Name:         "new-run",
					Context:      testValidationCtx,
					Status:       nico.MachineValidationStatus{State: testValidationDone, Total: 5, Completed: 5},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error (latest run passed): %v", err)
	}
}

// Coverage: deployDpuExtensionServices error path

func TestCreate_DpuExtensionServices_ErrorPath(t *testing.T) {
	instanceID := uuid.New().String()
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		updateInstance: func(
			ctx context.Context, org string, id string,
			req nico.InstanceUpdateRequest,
		) (*nico.Instance, *http.Response, error) {
			return nil, &http.Response{StatusCode: 500}, fmt.Errorf("internal server error")
		},
	}

	spec := validProviderSpec()
	spec.DpuExtensionServices = []v1beta1.DpuExtensionServiceSpec{
		{ServiceID: "svc-1", Version: "1.0.0"},
	}
	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error when DPU deployment fails")
	}
	if !strings.Contains(err.Error(), "DPU extension services") {
		t.Errorf("Expected error about DPU extension services, got: %v", err)
	}
}

// TenantAccountAPI: targeted instance creation capability

func TestCreate_TargetedCreation_EnabledViaTenantAccount(t *testing.T) {
	instanceID := uuid.New().String()
	siteID := testSiteID
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id:     &mid,
				Health: &nico.MachineHealth{Alerts: []nico.MachineHealthProbeAlert{}},
			}, &http.Response{StatusCode: 200}, nil
		},
		getAllTenantAccount: func(ctx context.Context, org string) ([]nico.TenantAccount, *http.Response, error) {
			status := nico.TENANTACCOUNTSTATUS_READY
			return []nico.TenantAccount{
				{
					Status: &status,
					SiteCapabilities: []nico.TenantAccountSiteCapability{
						{SiteIds: []string{siteID}, TargetedInstanceCreation: true},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID
	spec.SiteID = siteID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
}

func TestCreate_TargetedCreation_DisabledViaTenantAccount(t *testing.T) {
	siteID := testSiteID
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return nil, nil, fmt.Errorf("should not be called")
		},
		getAllTenantAccount: func(ctx context.Context, org string) ([]nico.TenantAccount, *http.Response, error) {
			status := nico.TENANTACCOUNTSTATUS_READY
			return []nico.TenantAccount{
				{
					Status: &status,
					SiteCapabilities: []nico.TenantAccountSiteCapability{
						{SiteIds: []string{siteID}, TargetedInstanceCreation: false},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID
	spec.SiteID = siteID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error when targeted creation is disabled")
	}
	if !strings.Contains(err.Error(), "targeted instance creation") {
		t.Errorf("Expected error about targeted instance creation, got: %v", err)
	}
}

func TestCreate_TargetedCreation_DefaultCapability(t *testing.T) {
	instanceID := uuid.New().String()
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id:     &mid,
				Health: &nico.MachineHealth{Alerts: []nico.MachineHealthProbeAlert{}},
			}, &http.Response{StatusCode: 200}, nil
		},
		getAllTenantAccount: func(ctx context.Context, org string) ([]nico.TenantAccount, *http.Response, error) {
			status := nico.TENANTACCOUNTSTATUS_READY
			return []nico.TenantAccount{
				{
					Status: &status,
					SiteCapabilities: []nico.TenantAccountSiteCapability{
						{TargetedInstanceCreation: true},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
}

func TestCreate_TargetedCreation_FallsBackToTenantAPI(t *testing.T) {
	instanceID := uuid.New().String()
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id:     &mid,
				Health: &nico.MachineHealth{Alerts: []nico.MachineHealthProbeAlert{}},
			}, &http.Response{StatusCode: 200}, nil
		},
		getCurrentTenant: func(ctx context.Context, org string) (*nico.Tenant, *http.Response, error) {
			caps := &nico.TenantCapabilities{}
			caps.SetTargetedInstanceCreation(true)
			return &nico.Tenant{Capabilities: caps}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
}

// Topology labels

func TestUpdate_TopologyLabels(t *testing.T) {
	instanceID := uuid.New().String()
	siteID := testSiteID
	nvlPartition := "nvl-part-1"
	nvlDomain := "nvl-domain-1"
	ibPartition := "ib-part-1"

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			inst.SiteId = &siteID
			inst.InfinibandInterfaces = []nico.InfiniBandInterface{
				{PartitionId: &ibPartition},
			}
			inst.NvLinkInterfaces = []nico.NVLinkInterface{
				{
					NvLinkLogicalPartitionId: &nvlPartition,
					NvLinkDomainId:           *nico.NewNullableString(&nvlDomain),
				},
			}
			return inst, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	}
	spec := validProviderSpec()
	machine := createTypedTestMachineWithStatus(spec, providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	labels := machine.GetLabels()
	if labels[topoLabelZone] != siteID {
		t.Errorf("zone = %q, want %q", labels[topoLabelZone], siteID)
	}
	if labels[topoLabelInstanceType] != spec.InstanceTypeID {
		t.Errorf("instance-type = %q, want %q",
			labels[topoLabelInstanceType], spec.InstanceTypeID)
	}
	if labels[topoLabelMachineID] != testMachineID {
		t.Errorf("machine-id = %q, want %q",
			labels[topoLabelMachineID], testMachineID)
	}
	if labels[topoLabelNVLinkPartition] != nvlPartition {
		t.Errorf("nvlink-partition = %q, want %q",
			labels[topoLabelNVLinkPartition], nvlPartition)
	}
	if labels[topoLabelNVLinkDomain] != nvlDomain {
		t.Errorf("nvlink-domain = %q, want %q",
			labels[topoLabelNVLinkDomain], nvlDomain)
	}
	if labels[topoLabelIBPartition] != ibPartition {
		t.Errorf("ib-partition = %q, want %q",
			labels[topoLabelIBPartition], ibPartition)
	}
}

func TestUpdate_TopologyLabels_MinimalInstance(t *testing.T) {
	instanceID := uuid.New().String()

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{InstanceID: &instanceID}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	labels := machine.GetLabels()
	if _, ok := labels[topoLabelNVLinkPartition]; ok {
		t.Error("nvlink-partition should not be set without NVLink interfaces")
	}
	if _, ok := labels[topoLabelIBPartition]; ok {
		t.Error("ib-partition should not be set without IB interfaces")
	}
	if labels[topoLabelMachineID] != testMachineID {
		t.Errorf("machine-id = %q, want %q",
			labels[topoLabelMachineID], testMachineID)
	}
}
