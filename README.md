# Machine API Provider for NICo

OpenShift Machine API actuator for provisioning bare-metal machines on NICo (NVIDIA NCX Infrastructure Controller) platform.

## Overview

This provider implements the OpenShift Machine API actuator interface for NICo, enabling:

- **Declarative machine provisioning** via `Machine` CRDs — generic (`instanceTypeId`) and targeted (`machineId`) allocation
- **Automated scaling** via `MachineSet` controllers
- **MachineHealthCheck remediation** — detects MHC-triggered deletions, reports a `k8s-mhc` health report to NICo's HealthReport API, and cleans it up on recovery
- **Health monitoring** — queries the HealthReport API (with JSONB fallback) and maps alerts to `MachineHealthy` / `NicoFaultRemediation` conditions
- **Pre-flight health and validation checks** — blocks targeted instance creation if the physical machine has critical faults or a failing validation run; escalates to `FailureReason` after `MaxFaultBlockedAttempts` (default 3)
- **Topology labels** — sets `topology.kubernetes.io/zone`, `node.kubernetes.io/instance-type`, `infra.nvidia.com/machine-id`, `infra.nvidia.com/nvlink-partition`, `infra.nvidia.com/nvlink-domain`, `infra.nvidia.com/infiniband-partition` for Kueue and Kai scheduler-aware GPU placement
- **DPU Extension Services** — deploys specified DPU services via `UpdateInstance` immediately after instance creation
- **BareMetalHost sync** — a separate polling controller syncs NICo machines to Metal3 `BareMetalHost` and `HostFirmwareComponents` CRs every 60 seconds
- **Admission webhook** — validates `NicoMachineProviderSpec` at admission time: required fields, UUID format, immutable fields, subnet count
- **Provisioning timeout** — sets `FailureReason` on the Machine after 30 minutes in a non-Ready state

The provider translates OpenShift Machine API requests into NICo REST API calls (via the NCX Infra Controller REST SDK), managing the full lifecycle of bare-metal instances.

## Architecture

```
+-----------------------------------------------------+
|         OpenShift Machine API Operator              |
|  +---------------+       +------------------+       |
|  |  Machine CRD  |-------|  MachineSet CRD  |       |
|  +-------+-------+       +--------+---------+       |
|          |                        |                 |
+----------+------------------------+-----------------+
           |                        |
           v                        v
+-----------------------------------------------------+
|   Machine API Provider for NICo (this repo)         |
|  +----------------------------------------------+   |
|  |  Machine Reconciler (controller)             |   |
|  |  +----------------------------------------+  |   |
|  |  |  Actuator                              |  |   |
|  |  |  - Create/Update/Delete/Exists         |  |   |
|  |  |  - Health monitoring & MHC remediation |  |   |
|  |  |  - Topology labels, DPU services       |  |   |
|  |  +----------+-----------------------------+  |   |
|  +-------------+--------------------------------+   |
|  +----------------------------------------------+   |
|  |  BareMetalHost Controller (poll every 60s)   |   |
|  |  - Syncs NICo machines → BMH + HFC CRs      |   |
|  +----------------------------------------------+   |
|  +----------------------------------------------+   |
|  |  Admission Webhook                           |   |
|  |  - Validates NicoMachineProviderSpec         |   |
|  +----------------------------------------------+   |
+----------------+------------------------------------+
                 |
                 v
+-----------------------------------------------------+
|         NICo REST API Client                        |
|   (github.com/NVIDIA/infra-controller)             |
+-----------------------------------------------------+
                 |
                 v
+-----------------------------------------------------+
|            NICo Platform                            |
|       (Bare-Metal Infrastructure Management)        |
+-----------------------------------------------------+
```

## Dependencies

- **[github.com/NVIDIA/infra-controller/rest-api/sdk/standard](https://github.com/NVIDIA/infra-controller)** - Auto-generated REST API client (SDK)
- **[github.com/openshift/api](https://github.com/openshift/api)** - OpenShift Machine API types
- **OpenShift 4.14+** or compatible Machine API implementation

### SDK Dependency Note

The Infra Controller REST SDK (`github.com/NVIDIA/infra-controller/rest-api/sdk/standard`)
does not publish sub-module-specific tags. `go.mod` pins to the commit corresponding
to the `v2.2.0-rc.2` upstream release via a pseudo-version:

```
github.com/NVIDIA/infra-controller/rest-api/sdk/standard v0.0.0-20260909164623-233d62db0072
```

Update this pseudo-version when NVIDIA cuts a new NICo release. Derive it with:

```bash
go get github.com/NVIDIA/infra-controller/rest-api/sdk/standard@<commit-sha>
```

## Prerequisites

1. OpenShift cluster (4.14+) or Kubernetes with Machine API CRDs installed
2. NICo API credentials (endpoint, orgName, token)
3. Access to NICo platform with configured Sites, VPCs, and Subnets

## Installation

### Option A: OLM (OpenShift)

Apply the File Based Catalog, then install from OperatorHub:

```bash
kubectl apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: nico-catalog
  namespace: openshift-marketplace
spec:
  sourceType: grpc
  image: ghcr.io/fabiendupont/machine-api-provider-nico-catalog:v0.1.0
  displayName: NICo Machine API Provider
EOF
```

The operator appears in OperatorHub as **Machine API Provider NICo**.

### Option B: Manual (kubectl)

```bash
# Build and push Docker image
make docker-build docker-push IMG=your-registry/machine-api-provider-nico:latest

# Deploy RBAC and controller
make deploy
```

### Create Credentials Secret

```bash
kubectl create secret generic nico-credentials \
  --namespace openshift-machine-api \
  --from-literal=endpoint="https://api.nico.nvidia.com" \
  --from-literal=orgName="your-org-name" \
  --from-literal=token="your-api-token"
```

## Usage

### Create a Machine

```yaml
apiVersion: machine.openshift.io/v1beta1
kind: Machine
metadata:
  name: worker-nico-1
  namespace: openshift-machine-api
  labels:
    machine.openshift.io/cluster-api-cluster: my-cluster
spec:
  providerSpec:
    value:
      apiVersion: nicoprovider.infrastructure.cluster.x-k8s.io/v1beta1
      kind: NicoMachineProviderSpec

      # NICo Site and Tenant
      siteId: "550e8400-e29b-41d4-a716-446655440000"
      tenantId: "660e8400-e29b-41d4-a716-446655440001"

      # Network Configuration
      vpcId: "770e8400-e29b-41d4-a716-446655440002"
      subnetId: "880e8400-e29b-41d4-a716-446655440003"

      # Instance Type (choose one approach)
      instanceTypeId: "990e8400-e29b-41d4-a716-446655440004"  # Generic instance type
      # OR
      # machineId: "aa0e8400-e29b-41d4-a716-446655440005"     # Specific machine

      # Optional: SSH Key Groups
      sshKeyGroupIds:
        - "bb0e8400-e29b-41d4-a716-446655440006"

      # Optional: Labels
      labels:
        environment: production
        role: worker

      # Optional: Cloud-init user data
      userData: |
        #cloud-config
        users:
          - name: core
            ssh_authorized_keys:
              - ssh-rsa AAAAB3NzaC1yc2E...

      # Credentials Secret
      credentialsSecret:
        name: nico-credentials
        namespace: openshift-machine-api
```

### Create a MachineSet for Auto-Scaling

```yaml
apiVersion: machine.openshift.io/v1beta1
kind: MachineSet
metadata:
  name: worker-nico-us-west
  namespace: openshift-machine-api
spec:
  replicas: 3
  selector:
    matchLabels:
      machine.openshift.io/cluster-api-machineset: worker-nico-us-west
  template:
    metadata:
      labels:
        machine.openshift.io/cluster-api-machineset: worker-nico-us-west
    spec:
      providerSpec:
        value:
          apiVersion: nicoprovider.infrastructure.cluster.x-k8s.io/v1beta1
          kind: NicoMachineProviderSpec
          siteId: "550e8400-e29b-41d4-a716-446655440000"
          tenantId: "660e8400-e29b-41d4-a716-446655440001"
          vpcId: "770e8400-e29b-41d4-a716-446655440002"
          subnetId: "880e8400-e29b-41d4-a716-446655440003"
          instanceTypeId: "990e8400-e29b-41d4-a716-446655440004"
          credentialsSecret:
            name: nico-credentials
            namespace: openshift-machine-api
```

### Multi-NIC Configuration

```yaml
spec:
  providerSpec:
    value:
      # ... other fields ...
      subnetId: "primary-subnet-uuid"
      additionalSubnetIds:
        - subnetId: "secondary-subnet-uuid"
          isPhysical: false
        - subnetId: "storage-subnet-uuid"
          isPhysical: true
```

## Provider Spec Reference

### NicoMachineProviderSpec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `siteId` | string | Yes | NICo Site UUID |
| `tenantId` | string | Yes | NICo Tenant ID |
| `vpcId` | string | Yes | VPC UUID for networking |
| `subnetId` | string | Yes | Primary subnet UUID |
| `instanceTypeId` | string | * | Instance type UUID (mutually exclusive with `machineId`) |
| `machineId` | string | * | Specific machine UUID for targeted provisioning |
| `allowUnhealthyMachine` | bool | No | Allow provisioning on unhealthy machines (requires capability) |
| `additionalSubnetIds` | []AdditionalSubnet | No | Additional network interfaces |
| `userData` | string | No | Cloud-init user data |
| `sshKeyGroupIds` | []string | No | SSH key group UUIDs |
| `labels` | map[string]string | No | Labels to apply to instance |
| `description` | string | No | Description for the NICo instance |
| `operatingSystemId` | string | No | NICo operating system UUID to install |
| `networkSecurityGroupId` | string | No | Network security group UUID to attach |
| `alwaysBootWithCustomIpxe` | bool | No | Always run iPXE script on reboot |
| `infiniBandInterfaces` | []InfiniBandInterfaceSpec | No | InfiniBand partition attachments |
| `nvLinkInterfaces` | []NVLinkInterfaceSpec | No | NVLink logical partition attachments |
| `dpuExtensionServices` | []DpuExtensionServiceSpec | No | DPU Extension Services to deploy after creation |
| `credentialsSecret` | CredentialsSecretReference | Yes | Secret containing API credentials |

\* Must specify exactly one of `instanceTypeId` or `machineId`

### NicoMachineProviderStatus

| Field | Type | Description |
|-------|------|-------------|
| `instanceId` | string | NICo instance UUID |
| `machineId` | string | Physical machine ID |
| `instanceState` | string | Instance lifecycle state (Pending, Provisioning, Configuring, Ready, etc.) |
| `addresses` | []MachineAddress | IP addresses assigned to the machine |
| `conditions` | []metav1.Condition | Instance lifecycle conditions: `InstanceAllocating`, `InstanceProvisioning`, `InstanceBootstrapping`, `InstanceReady`, `InstanceTerminating`, `InstanceError`, `InstanceProvisioned`, `MachineHealthy`, `NicoFaultRemediation`, `FaultBlockedCreation` |
| `healthLabels` | map[string]string | Health labels matching the CCM (`infra.nvidia.com/healthy`, `infra.nvidia.com/health-alert-count`) |

## Prometheus Metrics

All metrics are registered under the `nico_mapi_` namespace.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `nico_mapi_instance_provision_seconds` | Histogram | `instance_type` | Time from Create call to instance Ready state |
| `nico_mapi_api_latency_seconds` | Histogram | `method` | NICo API call latency per method |
| `nico_mapi_api_errors_total` | Counter | `method`, `status_code` | NICo API errors per method and HTTP status |
| `nico_mapi_machines_managed` | Gauge | — | Machines currently managed by this provider |
| `nico_mapi_machines_unhealthy` | Gauge | — | Machines with a `MachineHealthy=False` condition |
| `nico_mapi_health_events_ingested_total` | Counter | — | Successful `CreateOrUpdateMachineHealthReport` calls |

## Admission Webhook

A validating webhook rejects `Machine` objects with an invalid `NicoMachineProviderSpec` at admission time:

- **Required fields**: `siteId`, `tenantId`, `vpcId`, `subnetId`, and exactly one of `instanceTypeId` / `machineId`
- **UUID format**: All ID fields (`siteId`, `tenantId`, `vpcId`, `subnetId`, `instanceTypeId`, `machineId`, `operatingSystemId`, `networkSecurityGroupId`, all `additionalSubnetIds`, all `dpuExtensionServices[*].serviceId`) must be valid UUIDs
- **Immutability**: `siteId` and `tenantId` cannot change after the Machine is created

## BareMetalHost Sync

A separate polling controller syncs NICo machines to Metal3 CRs every 60 seconds. It is **disabled by default** — enable it with `--enable-bmh-sync=true` and provide credentials via the flags below.

| Flag | Default | Description |
|------|---------|-------------|
| `--enable-bmh-sync` | `false` | Enable the BMH sync controller |
| `--bmh-credentials-secret-name` | `nico-credentials` | Name of the credentials Secret |
| `--bmh-credentials-secret-namespace` | `openshift-machine-api` | Namespace of the credentials Secret |
| `--bmh-namespace` | `openshift-machine-api` | Namespace where BMH and HFC CRs are created |

The credentials Secret must have provider-admin scope (the same fields as the machine credentials: `endpoint`, `orgName`, `token`).

What gets synced:

- **BareMetalHost** — created with `externallyProvisioned: true`; includes BMC address (`redfish+https://`), boot MAC address from `evaluatedBootInterface` in the Site Explorer report (falls back to first NIC in machine metadata when Site Explorer data is unavailable), `Spec.Online` driven by machine status (`Ready`/`InUse` = true), hardware details annotation (system vendor, BIOS, NICs, CPU, RAM, storage from machine metadata and SKU), and labels `infra.nvidia.com/machine-id` + `infra.nvidia.com/site-id`
- **HostFirmwareComponents** — firmware versions from the Site Explorer (RMS integration), plus BMC firmware revision and per-GPU vBIOS (`gpu-N-vbios`)
- **SKU cache** — `GetAllSku` results are cached for 5 minutes to reduce API calls
- **403 degradation** — silently skips sync if the credential does not have provider-admin scope

## Development

### Building

```bash
make build          # Build binary
make test           # Run tests
make docker-build   # Build Docker image
make run            # Run locally (requires kubeconfig)
```

### Release Artifacts

```bash
# OLM bundle image
make bundle-build bundle-push

# FBC catalog image
make catalog-build catalog-push
```

### Project Structure

```
machine-api-provider-nico/
├── cmd/manager/          # Controller manager entry point
├── pkg/
│   ├── apis/             # NicoMachineProviderSpec types
│   ├── actuators/        # Machine actuator implementation
│   ├── metrics/          # Prometheus metrics
│   ├── providerid/       # Provider ID parsing and formatting
│   └── controllers/
│       ├── machine/      # Machine reconciler
│       └── baremetalhost/ # BareMetalHost and HostFirmwareComponents sync
├── config/               # Deployment manifests
│   ├── rbac/             # RBAC permissions
│   ├── manager/          # Controller deployment
│   └── samples/          # Example Machine CRs
├── bundle/               # OLM bundle (CSV)
├── catalog/              # File Based Catalog for OLM
└── Dockerfile            # Container build
```

## Troubleshooting

### Machine stuck in "Provisioning" state

Check the controller logs:

```bash
kubectl logs -n openshift-machine-api \
  -l app=machine-api-provider-nico \
  --tail=100 -f
```

Common issues:
- Invalid credentials in secret
- Incorrect site/tenant/VPC/subnet UUIDs
- Network connectivity to NICo API
- Instance type not available in site

### Instance created but not joining cluster

1. Verify user data is correctly formatted
2. Check instance has network connectivity
3. Verify SSH keys are configured
4. Check OpenShift ignition/bootstrap process

### Permission errors

Ensure the service account has proper RBAC:

```bash
kubectl auth can-i get machines.machine.openshift.io \
  --as=system:serviceaccount:openshift-machine-api:machine-api-provider-nico
```

## License

Apache 2.0

## Related Projects

- [cloud-provider-nvidia-ncx-infra-controller](../cloud-provider-nvidia-ncx-infra-controller) - Cloud Controller Manager for NICo
- [infra-controller](https://github.com/NVIDIA/infra-controller) - NICo Infrastructure Controller (REST API and SDK)

## Contributing

Contributions are welcome! Please submit issues and pull requests to the GitHub repository.
