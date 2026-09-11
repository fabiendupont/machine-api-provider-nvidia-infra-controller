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
	"strconv"
	"time"

	"github.com/google/uuid"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"

	nico "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
	v1beta1 "github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/apis/nicoprovider/v1beta1"
	nicometrics "github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/metrics"
	"github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/providerid"
)

// NicoClientInterface defines the methods needed from NICo REST client
type NicoClientInterface interface {
	CreateInstance(
		ctx context.Context, org string, req nico.InstanceCreateRequest,
	) (*nico.Instance, *http.Response, error)
	GetInstance(
		ctx context.Context, org string, instanceId string,
	) (*nico.Instance, *http.Response, error)
	DeleteInstance(
		ctx context.Context, org string, instanceId string,
		deleteReq *nico.InstanceDeleteRequest,
	) (*http.Response, error)
	UpdateInstance(
		ctx context.Context, org string, instanceId string,
		req nico.InstanceUpdateRequest,
	) (*nico.Instance, *http.Response, error)
	GetMachine(
		ctx context.Context, org string, machineId string,
	) (*nico.Machine, *http.Response, error)
	GetCurrentTenant(
		ctx context.Context, org string,
	) (*nico.Tenant, *http.Response, error)
	GetInstanceStatusHistory(
		ctx context.Context, org string, instanceId string,
	) ([]nico.StatusDetail, *http.Response, error)
	GetAllMachineHealthReport(
		ctx context.Context, org string, machineId string,
	) ([]nico.MachineHealthReportEntry, *http.Response, error)
	CreateOrUpdateMachineHealthReport(
		ctx context.Context, org string, machineId string,
		req nico.MachineHealthReportEntryRequest,
	) (*nico.MachineHealthReportEntry, *http.Response, error)
	DeleteMachineHealthReport(
		ctx context.Context, org string, machineId string, source string,
	) (*http.Response, error)
	GetMachineValidationRuns(
		ctx context.Context, org string, machineId string,
	) ([]nico.MachineValidationRun, *http.Response, error)
	GetMachineStatusHistory(
		ctx context.Context, org string, machineId string,
	) ([]nico.StatusDetail, *http.Response, error)
	GetAllTenantAccount(
		ctx context.Context, org string,
	) ([]nico.TenantAccount, *http.Response, error)
	GetAllMachine(
		ctx context.Context, org string,
	) ([]nico.Machine, *http.Response, error)
	GetAllSku(
		ctx context.Context, org string,
	) ([]nico.Sku, *http.Response, error)
	GetAllSiteExplorerEndpoint(
		ctx context.Context, org string,
	) ([]nico.ExploredEndpoint, *http.Response, error)
}

// nicoClient wraps the SDK APIClient and injects auth context
type nicoClient struct {
	client *nico.APIClient
	token  string
}

func (c *nicoClient) authCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, nico.ContextAccessToken, c.token)
}

func (c *nicoClient) CreateInstance(
	ctx context.Context, org string, req nico.InstanceCreateRequest,
) (*nico.Instance, *http.Response, error) {
	return c.client.InstanceAPI.CreateInstance(c.authCtx(ctx), org).InstanceCreateRequest(req).Execute()
}

func (c *nicoClient) GetInstance(
	ctx context.Context, org, instanceId string,
) (*nico.Instance, *http.Response, error) {
	return c.client.InstanceAPI.GetInstance(c.authCtx(ctx), org, instanceId).Execute()
}

func (c *nicoClient) DeleteInstance(
	ctx context.Context, org, instanceId string,
	deleteReq *nico.InstanceDeleteRequest,
) (*http.Response, error) {
	r := c.client.InstanceAPI.DeleteInstance(c.authCtx(ctx), org, instanceId)
	if deleteReq != nil {
		r = r.InstanceDeleteRequest(*deleteReq)
	}
	_, httpResp, err := r.Execute()
	return httpResp, err
}

func (c *nicoClient) UpdateInstance(
	ctx context.Context, org, instanceId string,
	req nico.InstanceUpdateRequest,
) (*nico.Instance, *http.Response, error) {
	return c.client.InstanceAPI.UpdateInstance(
		c.authCtx(ctx), org, instanceId,
	).InstanceUpdateRequest(req).Execute()
}

func (c *nicoClient) GetMachine(
	ctx context.Context, org, machineId string,
) (*nico.Machine, *http.Response, error) {
	return c.client.MachineAPI.GetMachine(c.authCtx(ctx), org, machineId).Execute()
}

func (c *nicoClient) GetCurrentTenant(
	ctx context.Context, org string,
) (*nico.Tenant, *http.Response, error) {
	return c.client.TenantAPI.GetCurrentTenant(c.authCtx(ctx), org).Execute()
}

func (c *nicoClient) GetInstanceStatusHistory(
	ctx context.Context, org, instanceId string,
) ([]nico.StatusDetail, *http.Response, error) {
	return c.client.InstanceAPI.GetInstanceStatusHistory(c.authCtx(ctx), org, instanceId).Execute()
}

func (c *nicoClient) GetAllMachineHealthReport(
	ctx context.Context, org, machineId string,
) ([]nico.MachineHealthReportEntry, *http.Response, error) {
	return c.client.HealthReportAPI.GetAllMachineHealthReport(c.authCtx(ctx), org, machineId).Execute()
}

func (c *nicoClient) CreateOrUpdateMachineHealthReport(
	ctx context.Context, org, machineId string,
	req nico.MachineHealthReportEntryRequest,
) (*nico.MachineHealthReportEntry, *http.Response, error) {
	return c.client.HealthReportAPI.CreateOrUpdateMachineHealthReport(
		c.authCtx(ctx), org, machineId,
	).MachineHealthReportEntryRequest(req).Execute()
}

func (c *nicoClient) DeleteMachineHealthReport(
	ctx context.Context, org, machineId, source string,
) (*http.Response, error) {
	return c.client.HealthReportAPI.DeleteMachineHealthReport(c.authCtx(ctx), org, machineId, source).Execute()
}

func (c *nicoClient) GetMachineValidationRuns(
	ctx context.Context, org, machineId string,
) ([]nico.MachineValidationRun, *http.Response, error) {
	return c.client.MachineValidationAPI.GetAllMachineValidationRuns(c.authCtx(ctx), org, machineId).Execute()
}

func (c *nicoClient) GetMachineStatusHistory(
	ctx context.Context, org, machineId string,
) ([]nico.StatusDetail, *http.Response, error) {
	return c.client.MachineAPI.GetMachineStatusHistory(c.authCtx(ctx), org, machineId).Execute()
}

func (c *nicoClient) GetAllTenantAccount(
	ctx context.Context, org string,
) ([]nico.TenantAccount, *http.Response, error) {
	return c.client.TenantAccountAPI.GetAllTenantAccount(c.authCtx(ctx), org).Execute()
}

func (c *nicoClient) GetAllMachine(
	ctx context.Context, org string,
) ([]nico.Machine, *http.Response, error) {
	return c.client.MachineAPI.GetAllMachine(c.authCtx(ctx), org).IncludeMetadata(true).Execute()
}

func (c *nicoClient) GetAllSku(
	ctx context.Context, org string,
) ([]nico.Sku, *http.Response, error) {
	return c.client.SKUAPI.GetAllSku(c.authCtx(ctx), org).Execute()
}

func (c *nicoClient) GetAllSiteExplorerEndpoint(
	ctx context.Context, org string,
) ([]nico.ExploredEndpoint, *http.Response, error) {
	return c.client.SiteExplorerAPI.GetAllSiteExplorerEndpoint(c.authCtx(ctx), org).Execute()
}

const (
	statusCodeUnknown = "unknown"

	severityCritical    = "critical"
	severityWarning     = "warning"
	severityRemediating = "remediating"

	condInstanceProvisioned  = "InstanceProvisioned"
	condMachineHealthy       = "MachineHealthy"
	condNicoFaultRemediation = "NicoFaultRemediation"

	labelHealthy    = "infra.nvidia.com/healthy"
	labelAlertCount = "infra.nvidia.com/health-alert-count"
	labelTrue       = "true"
	labelFalse      = "false"

	topoLabelZone            = "topology.kubernetes.io/zone"
	topoLabelInstanceType    = "node.kubernetes.io/instance-type"
	topoLabelMachineID       = "infra.nvidia.com/machine-id"
	topoLabelNVLinkPartition = "infra.nvidia.com/nvlink-partition"
	topoLabelNVLinkDomain    = "infra.nvidia.com/nvlink-domain"
	topoLabelIBPartition     = "infra.nvidia.com/infiniband-partition"
)

// APIErrorKind classifies NICo API errors for retry decisions.
type APIErrorKind int

const (
	// ErrorTransient indicates a retryable error (network, 429, 5xx).
	ErrorTransient APIErrorKind = iota
	// ErrorTerminal indicates a non-retryable error (400 bad request).
	ErrorTerminal
)

// ClassifiedError wraps an error with a classification for retry logic.
type ClassifiedError struct {
	Kind    APIErrorKind
	wrapped error
}

func (e *ClassifiedError) Error() string { return e.wrapped.Error() }
func (e *ClassifiedError) Unwrap() error { return e.wrapped }

// classifyHTTPError wraps an error with transient/terminal classification
// based on the HTTP status code.
func classifyHTTPError(httpResp *http.Response, err error) error {
	if httpResp == nil {
		return &ClassifiedError{Kind: ErrorTransient, wrapped: err}
	}
	switch httpResp.StatusCode {
	case 400:
		return &ClassifiedError{Kind: ErrorTerminal, wrapped: err}
	default:
		return &ClassifiedError{Kind: ErrorTransient, wrapped: err}
	}
}

// Actuator implements the OpenShift Machine actuator interface
type Actuator struct {
	client        client.Client
	eventRecorder events.EventRecorder
	// For testing
	nicoAPIClient NicoClientInterface
	orgName       string
}

// NewActuator creates a new machine actuator
func NewActuator(k8sClient client.Client, eventRecorder events.EventRecorder) *Actuator {
	return &Actuator{
		client:        k8sClient,
		eventRecorder: eventRecorder,
	}
}

// NewNicoAPIClient builds a NicoClientInterface from raw credentials.
// Used by controllers that need a NICo client at startup (e.g. BMH sync).
func NewNicoAPIClient(endpoint, token string) NicoClientInterface {
	sdkCfg := nico.NewConfiguration()
	sdkCfg.Servers = nico.ServerConfigurations{{URL: endpoint}}
	return &nicoClient{
		client: nico.NewAPIClient(sdkCfg),
		token:  token,
	}
}

// NewActuatorWithClient creates a new machine actuator with injected client (for testing)
func NewActuatorWithClient(
	k8sClient client.Client, eventRecorder events.EventRecorder,
	nicoAPIClient NicoClientInterface, orgName string,
) *Actuator {
	return &Actuator{
		client:        k8sClient,
		eventRecorder: eventRecorder,
		nicoAPIClient: nicoAPIClient,
		orgName:       orgName,
	}
}

// validateProviderSpec validates the provider spec fields before making API calls.
func validateProviderSpec(spec *v1beta1.NicoMachineProviderSpec) error {
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

// buildInstanceRequest constructs the API request body from a provider spec.
func buildInstanceRequest(
	name string,
	providerSpec *v1beta1.NicoMachineProviderSpec,
) nico.InstanceCreateRequest {
	interfaces := make([]nico.InterfaceCreateRequest, 0, 1+len(providerSpec.AdditionalSubnetIDs))
	interfaces = append(interfaces, nico.InterfaceCreateRequest{
		SubnetId:   &providerSpec.SubnetID,
		IsPhysical: ptr(false),
	})

	for _, additionalSubnet := range providerSpec.AdditionalSubnetIDs {
		subnetID := additionalSubnet.SubnetID
		interfaces = append(interfaces, nico.InterfaceCreateRequest{
			SubnetId:   &subnetID,
			IsPhysical: ptr(additionalSubnet.IsPhysical),
		})
	}

	req := nico.InstanceCreateRequest{
		Name:             name,
		TenantId:         providerSpec.TenantID,
		VpcId:            providerSpec.VpcID,
		Interfaces:       interfaces,
		PhoneHomeEnabled: ptr(true),
	}

	if providerSpec.InstanceTypeID != "" {
		req.InstanceTypeId = *nico.NewNullableString(&providerSpec.InstanceTypeID)
	}
	if providerSpec.MachineID != "" {
		req.MachineId = *nico.NewNullableString(&providerSpec.MachineID)
	}
	if providerSpec.AllowUnhealthyMachine {
		req.AllowUnhealthyMachine = ptr(true)
	}
	if providerSpec.UserData != "" {
		userData := providerSpec.UserData
		req.UserData = *nico.NewNullableString(&userData)
	}
	// The API requires either ipxeScript or operatingSystemId.
	if providerSpec.OperatingSystemID != "" {
		req.OperatingSystemId = *nico.NewNullableString(&providerSpec.OperatingSystemID)
	} else {
		ipxeScript := "#!ipxe\necho Booting via NICo"
		req.IpxeScript = *nico.NewNullableString(&ipxeScript)
	}
	if len(providerSpec.SSHKeyGroupIDs) > 0 {
		req.SshKeyGroupIds = providerSpec.SSHKeyGroupIDs
	}
	if len(providerSpec.Labels) > 0 {
		req.Labels = providerSpec.Labels
	}
	if providerSpec.NetworkSecurityGroupID != "" {
		req.NetworkSecurityGroupId = *nico.NewNullableString(&providerSpec.NetworkSecurityGroupID)
	}
	if providerSpec.Description != "" {
		desc := providerSpec.Description
		req.Description = *nico.NewNullableString(&desc)
	}
	if providerSpec.AlwaysBootWithCustomIpxe {
		req.AlwaysBootWithCustomIpxe = ptr(true)
	}
	if len(providerSpec.InfiniBandInterfaces) > 0 {
		ibInterfaces := make([]nico.InfiniBandInterfaceCreateRequest, 0, len(providerSpec.InfiniBandInterfaces))
		for _, ib := range providerSpec.InfiniBandInterfaces {
			ibReq := nico.InfiniBandInterfaceCreateRequest{}
			if ib.PartitionID != "" {
				ibReq.PartitionId = &ib.PartitionID
			}
			if ib.Device != "" {
				ibReq.Device = &ib.Device
			}
			if ib.IsPhysical {
				ibReq.IsPhysical = ptr(true)
			}
			if ib.DeviceInstance != nil {
				ibReq.DeviceInstance = ib.DeviceInstance
			}
			ibInterfaces = append(ibInterfaces, ibReq)
		}
		req.InfinibandInterfaces = ibInterfaces
	}
	if len(providerSpec.NVLinkInterfaces) > 0 {
		nvlInterfaces := make([]nico.NVLinkInterfaceCreateOrUpdateRequest, 0, len(providerSpec.NVLinkInterfaces))
		for _, nvl := range providerSpec.NVLinkInterfaces {
			nvlReq := nico.NVLinkInterfaceCreateOrUpdateRequest{}
			if nvl.NVLinkLogicalPartitionID != "" {
				nvlReq.NvLinkLogicalPartitionId = &nvl.NVLinkLogicalPartitionID
			}
			if nvl.DeviceInstance != nil {
				nvlReq.DeviceInstance = nvl.DeviceInstance
			}
			nvlInterfaces = append(nvlInterfaces, nvlReq)
		}
		req.NvLinkInterfaces = nvlInterfaces
	}

	return req
}

// Create provisions a new instance
func (a *Actuator) Create(ctx context.Context, machine runtime.Object) error {
	machineObj, ok := machine.(client.Object)
	if !ok {
		return fmt.Errorf("machine is not a client.Object")
	}

	// Parse provider spec
	providerSpec, err := a.getProviderSpec(machineObj)
	if err != nil {
		return fmt.Errorf("failed to get provider spec: %w", err)
	}

	// Validate provider spec
	if err := validateProviderSpec(providerSpec); err != nil {
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeWarning, "InvalidSpec", "", "Invalid provider spec: %v", err)
		}
		return fmt.Errorf("invalid provider spec: %w", err)
	}

	// Get NICo client and orgName
	nicoAPIClient, orgName, err := a.getNicoClient(ctx, providerSpec)
	if err != nil {
		return fmt.Errorf("failed to create NICo client: %w", err)
	}

	// Validate tenant capabilities for targeted provisioning
	if providerSpec.MachineID != "" {
		if !a.hasTargetedInstanceCreation(ctx, nicoAPIClient, orgName, providerSpec.SiteID) {
			return fmt.Errorf("tenant does not have targeted instance creation enabled; cannot use machineId")
		}
	}

	// Pre-flight health check for targeted provisioning
	if providerSpec.MachineID != "" && !providerSpec.AllowUnhealthyMachine {
		if blocked, blockErr := a.checkPreFlightHealth(ctx, nicoAPIClient, orgName, machineObj, providerSpec); blocked {
			return blockErr
		}
	}

	// Build instance request
	instanceReq := buildInstanceRequest(machineObj.GetName(), providerSpec)

	// Create instance
	createStart := time.Now()
	instance, httpResp, err := nicoAPIClient.CreateInstance(
		ctx, orgName, instanceReq,
	)
	nicometrics.APILatency.WithLabelValues("CreateInstance").Observe(time.Since(createStart).Seconds())
	if err != nil {
		return a.handleCreateError(machineObj, httpResp, err)
	}
	if instance == nil {
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeWarning, "FailedCreate", "",
				"Create instance returned no data")
		}
		return fmt.Errorf(
			"create instance returned no data, status code: %d",
			httpResp.StatusCode,
		)
	}

	// Build provider status
	providerStatus := &v1beta1.NicoMachineProviderStatus{
		InstanceID: instance.Id,
	}

	if instance.MachineId.Get() != nil {
		providerStatus.MachineID = instance.MachineId.Get()
	}
	if instance.Status != nil {
		status := string(*instance.Status)
		providerStatus.InstanceState = &status
	}

	// Extract addresses
	for _, iface := range instance.Interfaces {
		for _, ipAddr := range iface.IpAddresses {
			providerStatus.Addresses = append(providerStatus.Addresses, v1beta1.MachineAddress{
				Type:    "InternalIP",
				Address: ipAddr,
			})
		}
	}

	meta.SetStatusCondition(&providerStatus.Conditions, metav1.Condition{
		Type:    condInstanceProvisioned,
		Status:  metav1.ConditionTrue,
		Reason:  "InstanceCreated",
		Message: fmt.Sprintf("Instance %s created successfully", *instance.Id),
	})

	if err := a.setProviderStatus(ctx, machineObj, providerStatus); err != nil {
		return fmt.Errorf("failed to update provider status: %w", err)
	}

	// Set provider ID using the local providerid package
	instanceUUID, err := uuid.Parse(*instance.Id)
	if err != nil {
		return fmt.Errorf("failed to parse instance ID from response: %w", err)
	}
	pid := providerid.NewProviderID(orgName, providerSpec.TenantID, providerSpec.SiteID, instanceUUID)
	if err := a.setProviderID(ctx, machineObj, pid.String()); err != nil {
		return fmt.Errorf("failed to set provider ID: %w", err)
	}

	// Deploy DPU extension services if specified
	if len(providerSpec.DpuExtensionServices) > 0 {
		if err := a.deployDpuExtensionServices(
			ctx, nicoAPIClient, orgName,
			machineObj, *instance.Id, providerSpec,
		); err != nil {
			return err
		}
	}

	nicometrics.MachinesManaged.Inc()
	if a.eventRecorder != nil {
		a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeNormal, "Created", "", "Created instance %s", *instance.Id)
	}
	return nil
}

// Update updates an existing instance
func (a *Actuator) Update(ctx context.Context, machine runtime.Object) error {
	machineObj, ok := machine.(client.Object)
	if !ok {
		return fmt.Errorf("machine is not a client.Object")
	}

	// Parse provider spec
	providerSpec, err := a.getProviderSpec(machineObj)
	if err != nil {
		return fmt.Errorf("failed to get provider spec: %w", err)
	}

	// Get provider status
	providerStatus, err := a.getProviderStatus(machineObj)
	if err != nil {
		return fmt.Errorf("failed to get provider status: %w", err)
	}

	if providerStatus.InstanceID == nil {
		return fmt.Errorf("instance ID not set in provider status")
	}

	// Get NICo client and orgName
	nicoAPIClient, orgName, err := a.getNicoClient(ctx, providerSpec)
	if err != nil {
		return fmt.Errorf("failed to create NICo client: %w", err)
	}

	// Get current instance status
	getStart := time.Now()
	instance, httpResp, err := nicoAPIClient.GetInstance(ctx, orgName, *providerStatus.InstanceID)
	nicometrics.APILatency.WithLabelValues("GetInstance").Observe(time.Since(getStart).Seconds())
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	if instance == nil {
		return fmt.Errorf("get instance returned no data, status code: %d", httpResp.StatusCode)
	}

	// Update provider status with state machine tracking
	if instance.Status != nil {
		status := string(*instance.Status)
		providerStatus.InstanceState = &status
		setInstanceStateConditions(providerStatus, *instance.Status)

		// Record provision duration when instance reaches Ready
		if *instance.Status == nico.INSTANCESTATUS_READY {
			for _, c := range providerStatus.Conditions {
				if c.Type == condInstanceProvisioned && !c.LastTransitionTime.IsZero() {
					nicometrics.InstanceProvisionDuration.WithLabelValues("").Observe(
						time.Since(c.LastTransitionTime.Time).Seconds())
					break
				}
			}
		}
	}
	if instance.MachineId.Get() != nil {
		providerStatus.MachineID = instance.MachineId.Get()
	}

	// Set topology labels from instance data and persist before status mutations
	if a.setTopologyLabels(machineObj, instance, providerSpec) {
		if err := a.client.Update(ctx, machineObj); err != nil {
			return fmt.Errorf("failed to update machine labels: %w", err)
		}
	}

	// Check machine health if a physical machine is assigned
	a.updateMachineHealth(ctx, nicoAPIClient, orgName, instance, providerStatus)

	// Record status history as events when instance is stuck or in error
	a.checkStatusHistory(ctx, nicoAPIClient, orgName, machineObj, instance)

	// Check provisioning timeout
	a.checkProvisioningTimeout(machineObj, instance, providerStatus)

	// Update addresses
	providerStatus.Addresses = []v1beta1.MachineAddress{}
	for _, iface := range instance.Interfaces {
		for _, ipAddr := range iface.IpAddresses {
			providerStatus.Addresses = append(providerStatus.Addresses, v1beta1.MachineAddress{
				Type:    "InternalIP",
				Address: ipAddr,
			})
		}
	}

	if err := a.setProviderStatus(ctx, machineObj, providerStatus); err != nil {
		return fmt.Errorf("failed to update provider status: %w", err)
	}

	return nil
}

// Exists checks if instance exists
func (a *Actuator) Exists(ctx context.Context, machine runtime.Object) (bool, error) {
	machineObj, ok := machine.(client.Object)
	if !ok {
		return false, fmt.Errorf("machine is not a client.Object")
	}

	// Get provider status
	providerStatus, err := a.getProviderStatus(machineObj)
	if err != nil {
		return false, fmt.Errorf("failed to get provider status: %w", err)
	}

	if providerStatus.InstanceID == nil {
		return false, nil
	}

	// Parse provider spec
	providerSpec, err := a.getProviderSpec(machineObj)
	if err != nil {
		return false, fmt.Errorf("failed to get provider spec: %w", err)
	}

	// Get NICo client and orgName
	nicoAPIClient, orgName, err := a.getNicoClient(ctx, providerSpec)
	if err != nil {
		return false, fmt.Errorf("failed to create NICo client: %w", err)
	}

	// Check if instance exists
	instance, httpResp, err := nicoAPIClient.GetInstance(ctx, orgName, *providerStatus.InstanceID)
	if err != nil {
		if httpResp != nil && httpResp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, fmt.Errorf("failed to check instance existence: %w", err)
	}

	// Instance exists if we get a non-nil instance
	return instance != nil, nil
}

// Delete deprovisions the instance
func (a *Actuator) Delete(ctx context.Context, machine runtime.Object) error {
	machineObj, ok := machine.(client.Object)
	if !ok {
		return fmt.Errorf("machine is not a client.Object")
	}

	// Parse provider spec
	providerSpec, err := a.getProviderSpec(machineObj)
	if err != nil {
		return fmt.Errorf("failed to get provider spec: %w", err)
	}

	// Get provider status
	providerStatus, err := a.getProviderStatus(machineObj)
	if err != nil {
		return fmt.Errorf("failed to get provider status: %w", err)
	}

	if providerStatus.InstanceID == nil {
		// Nothing to delete
		return nil
	}

	// Get NICo client and orgName
	nicoAPIClient, orgName, err := a.getNicoClient(ctx, providerSpec)
	if err != nil {
		return fmt.Errorf("failed to create NICo client: %w", err)
	}

	// Check if this deletion is triggered by MachineHealthCheck remediation
	var deleteReq *nico.InstanceDeleteRequest
	if isMHCRemediation(machineObj) {
		now := time.Now().UTC()

		// Report a health issue via the HealthReport API so NICo's
		// remediation workflow can attempt repair.
		if providerStatus.MachineID != nil {
			a.reportMHCHealthIssue(ctx, nicoAPIClient, orgName, machineObj, providerStatus)
		}

		// Also set MachineHealthIssue on the delete request as fallback
		details := fmt.Sprintf(
			`{"machine_name":%q,"namespace":%q,"annotation":"machine.openshift.io/unhealthy","detected_at":%q}`,
			machineObj.GetName(), machineObj.GetNamespace(), now.Format(time.RFC3339),
		)
		summary := fmt.Sprintf("MachineHealthCheck triggered remediation for machine %s", machineObj.GetName())
		deleteReq = &nico.InstanceDeleteRequest{
			MachineHealthIssue: &nico.MachineHealthIssue{
				Category: "MachineHealthCheck",
				Summary:  *nico.NewNullableString(&summary),
				Details:  *nico.NewNullableString(&details),
			},
		}
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeNormal, "MHCRemediation", "",
				"Reporting machine health issue to NICo for break-fix workflow")
		}
	}

	// Delete instance
	deleteStart := time.Now()
	httpResp, err := nicoAPIClient.DeleteInstance(ctx, orgName, *providerStatus.InstanceID, deleteReq)
	nicometrics.APILatency.WithLabelValues("DeleteInstance").Observe(time.Since(deleteStart).Seconds())
	if err != nil {
		statusCode := statusCodeUnknown
		if httpResp != nil {
			statusCode = strconv.Itoa(httpResp.StatusCode)
		}
		// Treat 404 as success (instance already deleted)
		if httpResp != nil && httpResp.StatusCode == 404 {
			if a.eventRecorder != nil {
				a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeNormal, "Deleted", "",
					"Instance %s already deleted (404)", *providerStatus.InstanceID)
			}
		} else {
			nicometrics.APICallErrors.WithLabelValues("DeleteInstance", statusCode).Inc()
			if a.eventRecorder != nil {
				a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeWarning,
					"FailedDelete", "", "Failed to delete instance: %v", err)
			}
			return fmt.Errorf("failed to delete instance: %w", err)
		}
	} else if httpResp.StatusCode != 200 && httpResp.StatusCode != 202 &&
		httpResp.StatusCode != 204 && httpResp.StatusCode != 404 {
		// Accept 200 (OK), 202 (Accepted/async), 204 (No Content), 404 (already gone)
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeWarning, "FailedDelete", "",
				"Delete instance returned unexpected status: %d", httpResp.StatusCode)
		}
		return fmt.Errorf("delete instance returned unexpected status: %d", httpResp.StatusCode)
	}

	nicometrics.MachinesManaged.Dec()
	if a.eventRecorder != nil {
		a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeNormal, "Deleted", "",
			"Deleted instance %s", *providerStatus.InstanceID)
	}
	return nil
}

// Helper functions

func (a *Actuator) getProviderSpec(machine client.Object) (*v1beta1.NicoMachineProviderSpec, error) {
	var providerSpecBytes []byte

	switch m := machine.(type) {
	case *machinev1beta1.Machine:
		if m.Spec.ProviderSpec.Value == nil {
			return nil, fmt.Errorf("providerSpec.value is nil")
		}
		providerSpecBytes = m.Spec.ProviderSpec.Value.Raw
	case *unstructured.Unstructured:
		providerSpecValue, found, err := unstructured.NestedFieldCopy(m.Object, "spec", "providerSpec", "value")
		if err != nil || !found {
			return nil, fmt.Errorf("providerSpec.value not found: %w", err)
		}
		providerSpecBytes, err = json.Marshal(providerSpecValue)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal providerSpec: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported machine type: %T", machine)
	}

	providerSpec := &v1beta1.NicoMachineProviderSpec{}
	if err := json.Unmarshal(providerSpecBytes, providerSpec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal providerSpec: %w", err)
	}

	return providerSpec, nil
}

func (a *Actuator) getProviderStatus(machine client.Object) (*v1beta1.NicoMachineProviderStatus, error) {
	var providerStatusBytes []byte

	switch m := machine.(type) {
	case *machinev1beta1.Machine:
		if m.Status.ProviderStatus == nil {
			return &v1beta1.NicoMachineProviderStatus{}, nil
		}
		providerStatusBytes = m.Status.ProviderStatus.Raw
	case *unstructured.Unstructured:
		providerStatusValue, found, err := unstructured.NestedFieldCopy(m.Object, "status", "providerStatus")
		if err != nil {
			return nil, fmt.Errorf("failed to get providerStatus: %w", err)
		}
		if !found {
			return &v1beta1.NicoMachineProviderStatus{}, nil
		}
		providerStatusBytes, err = json.Marshal(providerStatusValue)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal providerStatus: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported machine type: %T", machine)
	}

	providerStatus := &v1beta1.NicoMachineProviderStatus{}
	if err := json.Unmarshal(providerStatusBytes, providerStatus); err != nil {
		return nil, fmt.Errorf("failed to unmarshal providerStatus: %w", err)
	}

	return providerStatus, nil
}

func (a *Actuator) setProviderStatus(
	ctx context.Context, machine client.Object,
	status *v1beta1.NicoMachineProviderStatus,
) error {
	statusBytes, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal status: %w", err)
	}

	switch m := machine.(type) {
	case *machinev1beta1.Machine:
		m.Status.ProviderStatus = &runtime.RawExtension{Raw: statusBytes}
		if err := a.client.Status().Update(ctx, m); err != nil {
			return fmt.Errorf("failed to update machine status: %w", err)
		}
	case *unstructured.Unstructured:
		var statusMap map[string]interface{}
		if err := json.Unmarshal(statusBytes, &statusMap); err != nil {
			return fmt.Errorf("failed to unmarshal status to map: %w", err)
		}
		if err := unstructured.SetNestedField(m.Object, statusMap, "status", "providerStatus"); err != nil {
			return fmt.Errorf("failed to set providerStatus: %w", err)
		}
		if err := a.client.Status().Update(ctx, m); err != nil {
			return fmt.Errorf("failed to update machine status: %w", err)
		}
	default:
		return fmt.Errorf("unsupported machine type: %T", machine)
	}

	return nil
}

func (a *Actuator) setProviderID(ctx context.Context, machine client.Object, providerID string) error {
	switch m := machine.(type) {
	case *machinev1beta1.Machine:
		m.Spec.ProviderID = &providerID
		if err := a.client.Update(ctx, m); err != nil {
			return fmt.Errorf("failed to update machine: %w", err)
		}
	case *unstructured.Unstructured:
		if err := unstructured.SetNestedField(m.Object, providerID, "spec", "providerID"); err != nil {
			return fmt.Errorf("failed to set providerID: %w", err)
		}
		if err := a.client.Update(ctx, m); err != nil {
			return fmt.Errorf("failed to update machine: %w", err)
		}
	default:
		return fmt.Errorf("unsupported machine type: %T", machine)
	}

	return nil
}

func (a *Actuator) getNicoClient(
	ctx context.Context, providerSpec *v1beta1.NicoMachineProviderSpec,
) (NicoClientInterface, string, error) {
	// Use injected client for testing
	if a.nicoAPIClient != nil {
		return a.nicoAPIClient, a.orgName, nil
	}

	// Fetch credentials secret
	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{
		Name:      providerSpec.CredentialsSecret.Name,
		Namespace: providerSpec.CredentialsSecret.Namespace,
	}

	if err := a.client.Get(ctx, secretKey, secret); err != nil {
		return nil, "", fmt.Errorf("failed to get credentials secret: %w", err)
	}

	// Validate secret contains required fields
	endpoint, ok := secret.Data["endpoint"]
	if !ok {
		return nil, "", fmt.Errorf("secret %s is missing 'endpoint' field", secretKey.Name)
	}
	orgName, ok := secret.Data["orgName"]
	if !ok {
		return nil, "", fmt.Errorf("secret %s is missing 'orgName' field", secretKey.Name)
	}
	token, ok := secret.Data["token"]
	if !ok {
		return nil, "", fmt.Errorf("secret %s is missing 'token' field", secretKey.Name)
	}

	// Create NICo API client
	sdkCfg := nico.NewConfiguration()
	sdkCfg.Servers = nico.ServerConfigurations{
		{URL: string(endpoint)},
	}

	return &nicoClient{
		client: nico.NewAPIClient(sdkCfg),
		token:  string(token),
	}, string(orgName), nil
}

// ptr is a helper function to get a pointer to a value
func ptr[T any](v T) *T {
	return &v
}

// setInstanceStateConditions maps NICo InstanceStatus values to provider
// status conditions. The SDK defines: Pending, Provisioning, Configuring,
// Ready, Updating, Rebooting, Terminating, Error.
func setInstanceStateConditions(
	providerStatus *v1beta1.NicoMachineProviderStatus,
	status nico.InstanceStatus,
) {
	switch status {
	case nico.INSTANCESTATUS_PENDING:
		setCondition(providerStatus, "InstanceAllocating", metav1.ConditionTrue,
			"Pending", "Instance creation request sent, awaiting allocation")
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Pending", "Instance is pending allocation")

	case nico.INSTANCESTATUS_PROVISIONING:
		setCondition(providerStatus, "InstanceAllocating", metav1.ConditionFalse,
			"Allocated", "Instance has been allocated")
		setCondition(providerStatus, "InstanceProvisioning", metav1.ConditionTrue,
			"Provisioning", "NICo is provisioning the machine")
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Provisioning", "Instance is being provisioned")

	case nico.INSTANCESTATUS_CONFIGURING:
		setCondition(providerStatus, "InstanceProvisioning", metav1.ConditionFalse,
			"Provisioned", "Machine has been provisioned")
		setCondition(providerStatus, "InstanceBootstrapping", metav1.ConditionTrue,
			"Configuring", "OS installed, configuration in progress")
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Configuring", "Instance is being configured")

	case nico.INSTANCESTATUS_READY:
		setCondition(providerStatus, "InstanceBootstrapping", metav1.ConditionFalse,
			"Complete", "Bootstrap completed")
		setCondition(providerStatus, "InstanceReady", metav1.ConditionTrue,
			"InstanceRunning", "Instance is fully operational")

	case nico.INSTANCESTATUS_UPDATING:
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Updating", "Instance is being updated")

	case nico.INSTANCESTATUS_REBOOTING:
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Rebooting", "Instance is rebooting")

	case nico.INSTANCESTATUS_TERMINATING:
		setCondition(providerStatus, "InstanceTerminating", metav1.ConditionTrue,
			"Terminating", "Instance deletion in progress")
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Terminating", "Instance is terminating")

	case nico.INSTANCESTATUS_ERROR:
		setCondition(providerStatus, "InstanceError", metav1.ConditionTrue,
			"Error", "Instance is in a terminal error state")
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Error", "Instance encountered an error")

	default:
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Unknown", fmt.Sprintf("Instance is in unknown state: %s", string(status)))
	}
}

func setCondition(
	providerStatus *v1beta1.NicoMachineProviderStatus,
	condType string, status metav1.ConditionStatus, reason, message string,
) {
	meta.SetStatusCondition(&providerStatus.Conditions, metav1.Condition{
		Type:    condType,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

// handleCreateError processes API errors from CreateInstance, recording
// metrics and events and returning a classified error.
func (a *Actuator) handleCreateError(
	machineObj client.Object, httpResp *http.Response, err error,
) error {
	statusCode := statusCodeUnknown
	if httpResp != nil {
		statusCode = strconv.Itoa(httpResp.StatusCode)
	}
	nicometrics.APICallErrors.WithLabelValues(
		"CreateInstance", statusCode,
	).Inc()

	errKind := "transient"
	classified := classifyHTTPError(httpResp, err)
	var ce *ClassifiedError
	if errors.As(classified, &ce) && ce.Kind == ErrorTerminal {
		errKind = "terminal"
	}

	if a.eventRecorder != nil {
		a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeWarning, "FailedCreate", "",
			"NICo API error (%s): %v", errKind, err)
	}

	var wrappedErr error
	if httpResp != nil && httpResp.Body != nil {
		respBody, _ := io.ReadAll(httpResp.Body)
		type apiError struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		var parsedErr apiError
		if json.Unmarshal(respBody, &parsedErr) == nil &&
			parsedErr.Message != "" {
			wrappedErr = fmt.Errorf(
				"failed to create instance (status %d): %s: %w",
				httpResp.StatusCode, parsedErr.Message, err,
			)
		} else {
			wrappedErr = fmt.Errorf(
				"failed to create instance (status %d): %w",
				httpResp.StatusCode, err,
			)
		}
	} else {
		wrappedErr = fmt.Errorf("failed to create instance: %w", err)
	}
	return classifyHTTPError(httpResp, wrappedErr)
}

// deployDpuExtensionServices deploys DPU extension services on a newly
// created instance via the UpdateInstance API.
func (a *Actuator) deployDpuExtensionServices(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	machineObj client.Object,
	instanceID string,
	providerSpec *v1beta1.NicoMachineProviderSpec,
) error {
	dpuDeployments := make(
		[]nico.DpuExtensionServiceDeploymentRequest,
		0, len(providerSpec.DpuExtensionServices),
	)
	for _, svc := range providerSpec.DpuExtensionServices {
		dpuReq := nico.DpuExtensionServiceDeploymentRequest{
			DpuExtensionServiceId: &svc.ServiceID,
		}
		if svc.Version != "" {
			dpuReq.Version = &svc.Version
		}
		dpuDeployments = append(dpuDeployments, dpuReq)
	}
	updateReq := nico.InstanceUpdateRequest{
		DpuExtensionServiceDeployments: dpuDeployments,
	}
	_, updateResp, updateErr := nicoAPIClient.UpdateInstance(
		ctx, orgName, instanceID, updateReq,
	)
	if updateErr != nil {
		statusCode := statusCodeUnknown
		if updateResp != nil {
			statusCode = strconv.Itoa(updateResp.StatusCode)
		}
		nicometrics.APICallErrors.WithLabelValues(
			"UpdateInstance", statusCode,
		).Inc()
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeWarning, "DPUDeployFailed", "",
				"Failed to deploy DPU extension services: %v",
				updateErr)
		}
		return fmt.Errorf(
			"failed to deploy DPU extension services: %w", updateErr,
		)
	}
	if a.eventRecorder != nil {
		a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeNormal, "DPUDeployed", "",
			"Deployed %d DPU extension service(s)",
			len(providerSpec.DpuExtensionServices))
	}
	return nil
}

// provisioningStuckThreshold is the duration after which a Provisioning instance
// is considered stuck and status history is emitted as Warning events.
const provisioningStuckThreshold = 5 * time.Minute

// ProvisioningTimeout is the maximum duration to wait for an instance to reach
// Ready state before setting FailureReason. Exported for testing.
var ProvisioningTimeout = 30 * time.Minute

// checkStatusHistory fetches the instance status history and records transitions
// as Warning events when the instance is in Error state or has been Provisioning
// for more than 5 minutes. This aids debugging without direct API access.
func (a *Actuator) checkStatusHistory(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	machineObj client.Object,
	instance *nico.Instance,
) {
	if instance.Status == nil || a.eventRecorder == nil {
		return
	}

	shouldFetch := false
	switch *instance.Status {
	case nico.INSTANCESTATUS_ERROR:
		shouldFetch = true
	case nico.INSTANCESTATUS_PROVISIONING:
		// Check if Provisioning for more than 5 minutes using status history
		shouldFetch = true
	default:
		return
	}

	if !shouldFetch {
		return
	}

	history, httpResp, err := nicoAPIClient.GetInstanceStatusHistory(ctx, orgName, *instance.Id)
	if err != nil || httpResp == nil || httpResp.StatusCode != http.StatusOK || len(history) == 0 {
		return
	}

	// For Provisioning state, check if it's been stuck for more than the threshold
	if *instance.Status == nico.INSTANCESTATUS_PROVISIONING {
		stuck := false
		for _, entry := range history {
			if entry.Status != nil && *entry.Status == string(nico.INSTANCESTATUS_PROVISIONING) && entry.Created != nil {
				if time.Since(*entry.Created) > provisioningStuckThreshold {
					stuck = true
				}
				break
			}
		}
		if !stuck {
			return
		}
		a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeWarning, "ProvisioningStuck", "",
			"Instance has been in Provisioning state for more than %s", provisioningStuckThreshold)

		// Also fetch physical machine status history if a machine is assigned
		if machineID, ok := instance.GetMachineIdOk(); ok && machineID != nil && *machineID != "" {
			a.emitMachineStatusHistory(ctx, nicoAPIClient, orgName, machineObj, *machineID)
		}
	}

	// Record status transitions as Warning events for debugging
	for _, entry := range history {
		status := "unknown"
		if entry.Status != nil {
			status = *entry.Status
		}
		msg := ""
		if entry.Message.Get() != nil {
			msg = *entry.Message.Get()
		}
		ts := ""
		if entry.Created != nil {
			ts = entry.Created.Format(time.RFC3339)
		}
		if msg != "" {
			a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeWarning, "StatusHistory", "",
				"[%s] %s: %s", ts, status, msg)
		} else {
			a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeWarning, "StatusHistory", "",
				"[%s] %s", ts, status)
		}
	}
}

// emitMachineStatusHistory fetches physical machine status transitions and
// records them as MachineStatusHistory Warning events for debugging. Non-fatal.
func (a *Actuator) emitMachineStatusHistory(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	machineObj client.Object,
	machineID string,
) {
	if a.eventRecorder == nil {
		return
	}
	history, httpResp, err := nicoAPIClient.GetMachineStatusHistory(ctx, orgName, machineID)
	if err != nil || httpResp == nil || httpResp.StatusCode != http.StatusOK || len(history) == 0 {
		return
	}
	for _, entry := range history {
		status := "unknown"
		if entry.Status != nil {
			status = *entry.Status
		}
		msg := ""
		if entry.Message.Get() != nil {
			msg = *entry.Message.Get()
		}
		ts := ""
		if entry.Created != nil {
			ts = entry.Created.Format(time.RFC3339)
		}
		if msg != "" {
			a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeWarning, "MachineStatusHistory", "",
				"[%s] machine %s: %s: %s", ts, machineID, status, msg)
		} else {
			a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeWarning, "MachineStatusHistory", "",
				"[%s] machine %s: %s", ts, machineID, status)
		}
	}
}

// checkProvisioningTimeout sets FailureReason on the Machine if the instance
// has been stuck in a non-Ready state beyond ProvisioningTimeout.
func (a *Actuator) checkProvisioningTimeout(
	machineObj client.Object,
	instance *nico.Instance,
	providerStatus *v1beta1.NicoMachineProviderStatus,
) {
	if instance.Status == nil {
		return
	}
	// Only check timeout for non-terminal, non-ready states
	switch *instance.Status {
	case nico.INSTANCESTATUS_PENDING, nico.INSTANCESTATUS_PROVISIONING, nico.INSTANCESTATUS_CONFIGURING:
		// continue
	default:
		return
	}

	// Find the InstanceProvisioned condition set during Create
	for _, c := range providerStatus.Conditions {
		if c.Type == condInstanceProvisioned && !c.LastTransitionTime.IsZero() {
			if time.Since(c.LastTransitionTime.Time) > ProvisioningTimeout {
				setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
					"ProvisioningTimeout",
					fmt.Sprintf("Instance has not reached Ready state after %s", ProvisioningTimeout))
				// Set failure on the Machine object if it's a typed Machine
				if m, ok := machineObj.(*machinev1beta1.Machine); ok {
					reason := "ProvisioningTimeout"
					msg := fmt.Sprintf("Instance stuck in %s state for more than %s", string(*instance.Status), ProvisioningTimeout)
					m.Status.ErrorReason = (*machinev1beta1.MachineStatusError)(&reason)
					m.Status.ErrorMessage = &msg
				}
				if a.eventRecorder != nil {
					a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeWarning, "ProvisioningTimeout", "",
						"Instance has not reached Ready state after %s, setting FailureReason", ProvisioningTimeout)
				}
			}
			break
		}
	}
}

// hasTargetedInstanceCreation checks if the tenant has targeted instance
// creation enabled for the given site. Tries the TenantAccount API first
// (checks SiteCapabilities), falls back to the deprecated
// TenantCapabilities on GetCurrentTenant.
func (a *Actuator) hasTargetedInstanceCreation(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	siteID string,
) bool {
	accounts, httpResp, err := nicoAPIClient.GetAllTenantAccount(ctx, orgName)
	if err == nil && httpResp != nil && httpResp.StatusCode < 300 {
		for _, acct := range accounts {
			if acct.GetStatus() != nico.TENANTACCOUNTSTATUS_READY {
				continue
			}
			for _, cap := range acct.SiteCapabilities {
				if !cap.TargetedInstanceCreation {
					continue
				}
				if len(cap.SiteIds) == 0 {
					return true
				}
				for _, id := range cap.SiteIds {
					if id == siteID {
						return true
					}
				}
			}
		}
		return false
	}

	// Fall back to deprecated TenantCapabilities
	tenant, _, tenantErr := nicoAPIClient.GetCurrentTenant(ctx, orgName)
	if tenantErr != nil || tenant == nil || tenant.Capabilities == nil {
		return true
	}
	if tenant.Capabilities.TargetedInstanceCreation != nil {
		return *tenant.Capabilities.TargetedInstanceCreation
	}
	return true
}

// isMHCRemediation detects if the Machine deletion was triggered by
// OpenShift MachineHealthCheck remediation. MHC sets the annotation
// "machine.openshift.io/unhealthy" on the Machine before requesting deletion.
func isMHCRemediation(machine client.Object) bool {
	annotations := machine.GetAnnotations()
	if annotations == nil {
		return false
	}
	_, hasUnhealthy := annotations["machine.openshift.io/unhealthy"]
	return hasUnhealthy
}

// setTopologyLabels sets infrastructure topology labels on the Machine
// object so they propagate to the Node via the Machine API controller.
// Returns true if any labels changed.
func (a *Actuator) setTopologyLabels(
	machineObj client.Object,
	instance *nico.Instance,
	providerSpec *v1beta1.NicoMachineProviderSpec,
) bool {
	labels := machineObj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	changed := false
	set := func(key, value string) {
		if value != "" && labels[key] != value {
			labels[key] = value
			changed = true
		}
	}

	if instance.SiteId != nil {
		set(topoLabelZone, *instance.SiteId)
	}
	if providerSpec.InstanceTypeID != "" {
		set(topoLabelInstanceType, providerSpec.InstanceTypeID)
	}
	if mid := instance.MachineId.Get(); mid != nil {
		set(topoLabelMachineID, *mid)
	}
	if len(instance.NvLinkInterfaces) > 0 {
		nvl := instance.NvLinkInterfaces[0]
		if nvl.NvLinkLogicalPartitionId != nil {
			set(topoLabelNVLinkPartition, *nvl.NvLinkLogicalPartitionId)
		}
		if did := nvl.NvLinkDomainId.Get(); did != nil {
			set(topoLabelNVLinkDomain, *did)
		}
	}
	if len(instance.InfinibandInterfaces) > 0 {
		ib := instance.InfinibandInterfaces[0]
		if ib.PartitionId != nil {
			set(topoLabelIBPartition, *ib.PartitionId)
		}
	}

	if changed {
		machineObj.SetLabels(labels)
	}
	return changed
}

// MaxFaultBlockedAttempts is the maximum number of consecutive fault-blocked
// Create attempts before setting FailureReason on the Machine. Exported for testing.
var MaxFaultBlockedAttempts = 3

// checkPreFlightHealth queries machine health before instance creation for
// targeted allocations. Tries the structured HealthAPI (ListFaultEvents)
// first, falls back to GetMachine().Health. Returns (true, error) if creation
// should be blocked, (false, nil) if creation can proceed. After
// MaxFaultBlockedAttempts consecutive blocks, sets FailureReason/FailureMessage.
func (a *Actuator) checkPreFlightHealth(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	machineObj client.Object,
	providerSpec *v1beta1.NicoMachineProviderSpec,
) (bool, error) {
	hasCritical, alertMsg := a.checkCriticalFaults(ctx, nicoAPIClient, orgName, providerSpec.MachineID)
	if !hasCritical {
		hasValidationFailure, validationMsg := a.checkMachineValidation(ctx, nicoAPIClient, orgName, providerSpec.MachineID)
		if !hasValidationFailure {
			return false, nil
		}
		alertMsg = validationMsg
	}

	// Read existing provider status to track attempt count
	providerStatus, _ := a.getProviderStatus(machineObj)
	attempt := 1
	for _, c := range providerStatus.Conditions {
		if c.Type == "FaultBlockedCreation" && c.Status == metav1.ConditionTrue {
			_, _ = fmt.Sscanf(c.Reason, "FaultBlocked_%d", &attempt)
			attempt++
			break
		}
	}

	setCondition(providerStatus, "FaultBlockedCreation", metav1.ConditionTrue,
		fmt.Sprintf("FaultBlocked_%d", attempt),
		fmt.Sprintf("Attempt %d of %d: %s", attempt, MaxFaultBlockedAttempts, alertMsg))
	setCondition(providerStatus, condMachineHealthy, metav1.ConditionFalse,
		"CriticalFault", alertMsg)

	// After max attempts, escalate to FailureReason
	if attempt >= MaxFaultBlockedAttempts {
		if m, ok := machineObj.(*machinev1beta1.Machine); ok {
			reason := "PreFlightHealthCheckFailed"
			msg := fmt.Sprintf("Machine %s has persistent critical faults after %d attempts: %s",
				providerSpec.MachineID, attempt, alertMsg)
			m.Status.ErrorReason = (*machinev1beta1.MachineStatusError)(&reason)
			m.Status.ErrorMessage = &msg
		}
	}

	if setErr := a.setProviderStatus(ctx, machineObj, providerStatus); setErr != nil {
		return true, fmt.Errorf("failed to update provider status: %w", setErr)
	}

	if a.eventRecorder != nil {
		a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeWarning, "FaultBlockedCreation", "",
			"Instance creation blocked: %s (attempt %d/%d)", alertMsg, attempt, MaxFaultBlockedAttempts)
	}

	return true, fmt.Errorf("instance creation blocked by critical health faults on machine %s (attempt %d/%d)",
		providerSpec.MachineID, attempt, MaxFaultBlockedAttempts)
}

// reportMHCHealthIssue posts a health report to NICo's HealthReport API
// when MachineHealthCheck triggers remediation. Non-fatal — failure is logged
// but does not block deletion.
func (a *Actuator) reportMHCHealthIssue(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	machineObj client.Object,
	providerStatus *v1beta1.NicoMachineProviderStatus,
) {
	alert := nico.MachineHealthProbeAlert{
		Id:              "mhc-remediation-triggered",
		Message:         fmt.Sprintf("MachineHealthCheck triggered remediation for machine %s", machineObj.GetName()),
		Classifications: []string{severityCritical},
	}
	reportReq := nico.MachineHealthReportEntryRequest{
		Source: "k8s-mhc",
		Alerts: []nico.MachineHealthProbeAlert{alert},
		Mode:   "replace",
	}

	_, resp, err := nicoAPIClient.CreateOrUpdateMachineHealthReport(
		ctx, orgName, *providerStatus.MachineID, reportReq,
	)
	if err == nil && resp != nil && resp.StatusCode < 300 {
		nicometrics.HealthEventsIngested.Inc()
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj, nil, corev1.EventTypeNormal, "MHCHealthReported", "",
				"Reported MHC health issue to NICo HealthReport API")
		}
	}
}

// checkCriticalFaults checks for open critical faults on a machine. Tries the
// HealthReport API first, falls back to GetMachine().Health JSONB.
// Returns (true, message) if critical faults are present.
func (a *Actuator) checkCriticalFaults(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	machineID string,
) (bool, string) {
	// Try HealthReport API first
	reports, httpResp, err := nicoAPIClient.GetAllMachineHealthReport(ctx, orgName, machineID)
	if err == nil && httpResp != nil && httpResp.StatusCode < 300 {
		var allAlerts []nico.MachineHealthProbeAlert
		for _, report := range reports {
			allAlerts = append(allAlerts, report.GetAlerts()...)
		}
		critical, _ := classifyAlerts(allAlerts)
		if len(critical) == 0 {
			return false, ""
		}
		alertMsg := fmt.Sprintf("Machine %s has %d critical health alert(s)", machineID, len(critical))
		if critical[0].Message != "" {
			alertMsg = critical[0].Message
		}
		return true, alertMsg
	}

	// Fall back to JSONB health parsing
	machine, mhResp, mhErr := nicoAPIClient.GetMachine(ctx, orgName, machineID)
	if mhErr != nil || mhResp == nil || mhResp.StatusCode != http.StatusOK || machine == nil {
		return false, ""
	}
	if machine.Health == nil || len(machine.Health.Alerts) == 0 {
		return false, ""
	}
	critical, _ := classifyAlerts(machine.Health.Alerts)
	if len(critical) == 0 {
		return false, ""
	}
	alertMsg := fmt.Sprintf("Machine %s has %d critical health alert(s)", machineID, len(critical))
	if critical[0].Message != "" {
		alertMsg = critical[0].Message
	}
	return true, alertMsg
}

// checkMachineValidation queries the most recent machine validation run.
// Returns (true, message) if the latest run has failures, (false, "") if
// validation passed or the API is unavailable.
func (a *Actuator) checkMachineValidation(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	machineID string,
) (bool, string) {
	runs, httpResp, err := nicoAPIClient.GetMachineValidationRuns(ctx, orgName, machineID)
	if err != nil || httpResp == nil || httpResp.StatusCode >= 300 || len(runs) == 0 {
		return false, ""
	}

	// Find the most recent run by EndTime
	latest := runs[0]
	for _, r := range runs[1:] {
		if r.EndTime.Get() != nil && (latest.EndTime.Get() == nil || r.EndTime.Get().After(*latest.EndTime.Get())) {
			latest = r
		}
	}

	state := latest.Status.State
	if state != "completed" {
		return true, fmt.Sprintf("Machine %s has a validation run in state %q", machineID, state)
	}
	if latest.Status.Completed < latest.Status.Total {
		return true, fmt.Sprintf("Machine %s validation: %d/%d tests passed",
			machineID, latest.Status.Completed, latest.Status.Total)
	}
	return false, ""
}

// classifyAlerts splits alerts into critical and warning based on their
// Classifications field. An alert is critical if any classification contains
// "critical"; warning if any classification contains "warning". Alerts with
// no recognized classification default to critical (fail-safe).
func classifyAlerts(alerts []nico.MachineHealthProbeAlert) (critical, warning []nico.MachineHealthProbeAlert) {
	for _, alert := range alerts {
		severity := classifyAlert(alert)
		switch severity {
		case severityWarning:
			warning = append(warning, alert)
		default:
			critical = append(critical, alert)
		}
	}
	return
}

// classifyAlert returns severityCritical or severityWarning based on the
// alert's Classifications slice. Defaults to critical (fail-safe).
func classifyAlert(alert nico.MachineHealthProbeAlert) string {
	hasCritical := false
	hasWarning := false
	for _, c := range alert.Classifications {
		switch c {
		case severityCritical:
			hasCritical = true
		case severityWarning:
			hasWarning = true
		}
	}
	if hasCritical {
		return severityCritical
	}
	if hasWarning {
		return severityWarning
	}
	return severityCritical
}

// hasRemediatingAlert returns true if any alert has a "remediating"
// classification.
func hasRemediatingAlert(alerts []nico.MachineHealthProbeAlert) bool {
	for _, alert := range alerts {
		for _, c := range alert.Classifications {
			if c == severityRemediating {
				return true
			}
		}
	}
	return false
}

// updateMachineHealth queries health reports via the HealthReport API and
// maps them to MachineHealthy and NicoFaultRemediation conditions. Falls back
// to the legacy GetMachine().Health JSONB approach if the HealthReport API is
// unavailable. Health failures are non-fatal.
func (a *Actuator) updateMachineHealth(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	instance *nico.Instance,
	providerStatus *v1beta1.NicoMachineProviderStatus,
) {
	machineID, ok := instance.GetMachineIdOk()
	if !ok || machineID == nil || *machineID == "" {
		return
	}

	// Snapshot previous health state for gauge tracking
	wasUnhealthy := isConditionFalse(providerStatus, condMachineHealthy)

	// Try HealthReport API first, fall back to JSONB
	if a.updateMachineHealthFromReports(ctx, nicoAPIClient, orgName, *machineID, providerStatus, wasUnhealthy) {
		trackUnhealthyGauge(wasUnhealthy, providerStatus)
		return
	}

	// Fall back to legacy JSONB health parsing
	a.updateMachineHealthFromJSONB(ctx, nicoAPIClient, orgName, *machineID, providerStatus)
	trackUnhealthyGauge(wasUnhealthy, providerStatus)
}

// isConditionFalse returns true if the named condition exists and is False.
func isConditionFalse(
	providerStatus *v1beta1.NicoMachineProviderStatus,
	condType string,
) bool {
	for _, c := range providerStatus.Conditions {
		if c.Type == condType {
			return c.Status == metav1.ConditionFalse
		}
	}
	return false
}

// trackUnhealthyGauge adjusts the MachinesUnhealthy gauge when the
// MachineHealthy condition transitions between True and False.
func trackUnhealthyGauge(
	wasUnhealthy bool,
	providerStatus *v1beta1.NicoMachineProviderStatus,
) {
	isUnhealthy := isConditionFalse(providerStatus, condMachineHealthy)
	if !wasUnhealthy && isUnhealthy {
		nicometrics.MachinesUnhealthy.Inc()
	} else if wasUnhealthy && !isUnhealthy {
		nicometrics.MachinesUnhealthy.Dec()
	}
}

// updateMachineHealthFromReports queries GetAllMachineHealthReport for health
// reports on the machine. Returns true if the API was available (even if no
// alerts), false if the API is unavailable and the caller should fall back.
// When the machine transitions from unhealthy to healthy, cleans up any
// k8s-mhc health report we previously created.
func (a *Actuator) updateMachineHealthFromReports(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	machineID string,
	providerStatus *v1beta1.NicoMachineProviderStatus,
	wasUnhealthy bool,
) bool {
	reports, httpResp, err := nicoAPIClient.GetAllMachineHealthReport(ctx, orgName, machineID)
	if err != nil || httpResp == nil || httpResp.StatusCode >= 300 {
		return false
	}

	// Collect all alerts across all report entries
	var allAlerts []nico.MachineHealthProbeAlert
	for _, report := range reports {
		allAlerts = append(allAlerts, report.GetAlerts()...)
	}

	if len(allAlerts) == 0 {
		setCondition(providerStatus, condMachineHealthy, metav1.ConditionTrue,
			"Healthy", "No health alerts")
		providerStatus.HealthLabels = map[string]string{
			labelHealthy: labelTrue,
		}
		setCondition(providerStatus, condNicoFaultRemediation, metav1.ConditionFalse,
			"NoRemediation", "No active fault remediation")
	} else {

		critical, warning := classifyAlerts(allAlerts)

		if len(critical) > 0 {
			reason := "CriticalFault"
			msg := fmt.Sprintf("Machine has %d critical health alert(s)", len(critical))
			if len(critical[0].Classifications) > 0 {
				reason = critical[0].Classifications[0]
			}
			if critical[0].Message != "" {
				msg = critical[0].Message
			}
			setCondition(providerStatus, condMachineHealthy, metav1.ConditionFalse,
				reason, msg)
			providerStatus.HealthLabels = map[string]string{
				labelHealthy:    labelFalse,
				labelAlertCount: fmt.Sprintf("%d", len(allAlerts)),
			}
		} else if len(warning) > 0 {
			warnMsg := fmt.Sprintf("Machine has %d warning alert(s)", len(warning))
			setCondition(providerStatus, condMachineHealthy, metav1.ConditionTrue,
				"HealthyWithWarnings", warnMsg)
			providerStatus.HealthLabels = map[string]string{
				labelHealthy:    labelTrue,
				labelAlertCount: fmt.Sprintf("%d", len(warning)),
			}
		}

		if hasRemediatingAlert(allAlerts) {
			setCondition(providerStatus, condNicoFaultRemediation, metav1.ConditionTrue,
				"RemediationInProgress", "Automated fault remediation in progress")
		} else {
			setCondition(providerStatus, condNicoFaultRemediation, metav1.ConditionFalse,
				"NoRemediation", "No active fault remediation")
		}

	} // end else (has alerts)

	// Clean up k8s-mhc health report on unhealthy→healthy transition
	isUnhealthy := isConditionFalse(providerStatus, condMachineHealthy)
	if wasUnhealthy && !isUnhealthy {
		resp, delErr := nicoAPIClient.DeleteMachineHealthReport(ctx, orgName, machineID, "k8s-mhc")
		if delErr == nil && resp != nil && resp.StatusCode < 300 {
			if a.eventRecorder != nil {
				a.eventRecorder.Eventf(nil, nil, corev1.EventTypeNormal, "MHCHealthReportCleared", "",
					"Cleared k8s-mhc health report for machine %s after recovery", machineID)
			}
		}
	}

	return true
}

// updateMachineHealthFromJSONB is the legacy fallback that parses health
// alerts from GetMachine().Health when the structured fault events API is
// unavailable.
func (a *Actuator) updateMachineHealthFromJSONB(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	machineID string,
	providerStatus *v1beta1.NicoMachineProviderStatus,
) {
	machine, httpResp, err := nicoAPIClient.GetMachine(ctx, orgName, machineID)
	if err != nil || httpResp == nil || httpResp.StatusCode != http.StatusOK || machine == nil {
		return
	}

	if machine.Health == nil {
		return
	}

	if len(machine.Health.Alerts) == 0 {
		setCondition(providerStatus, condMachineHealthy, metav1.ConditionTrue,
			"Healthy", "Machine has no health alerts")
		providerStatus.HealthLabels = map[string]string{
			labelHealthy: labelTrue,
		}
		setCondition(providerStatus, condNicoFaultRemediation, metav1.ConditionFalse,
			"NoRemediation", "No active fault remediation")
		return
	}

	critical, warning := classifyAlerts(machine.Health.Alerts)

	if len(critical) > 0 {
		reason := "CriticalFault"
		msg := fmt.Sprintf("Machine has %d critical health alert(s)", len(critical))
		if critical[0].Message != "" {
			msg = critical[0].Message
		}
		if len(critical[0].Classifications) > 0 {
			reason = critical[0].Classifications[0]
		}
		setCondition(providerStatus, condMachineHealthy, metav1.ConditionFalse,
			reason, msg)
		providerStatus.HealthLabels = map[string]string{
			labelHealthy:    labelFalse,
			labelAlertCount: fmt.Sprintf("%d", len(machine.Health.Alerts)),
		}
	} else if len(warning) > 0 {
		warnMsg := fmt.Sprintf("Machine has %d warning alert(s)", len(warning))
		setCondition(providerStatus, condMachineHealthy, metav1.ConditionTrue,
			"HealthyWithWarnings", warnMsg)
		providerStatus.HealthLabels = map[string]string{
			labelHealthy:    labelTrue,
			labelAlertCount: fmt.Sprintf("%d", len(warning)),
		}
	}

	if hasRemediatingAlert(machine.Health.Alerts) {
		setCondition(providerStatus, condNicoFaultRemediation, metav1.ConditionTrue,
			"RemediationInProgress", "Automated fault remediation in progress")
	} else {
		setCondition(providerStatus, condNicoFaultRemediation, metav1.ConditionFalse,
			"NoRemediation", "No active fault remediation")
	}
}
