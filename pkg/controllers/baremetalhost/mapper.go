package baremetalhost

import (
	"encoding/json"
	"fmt"

	metal3 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nico "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

const (
	hardwareDetailsAnnotation = "inspect.metal3.io/hardwaredetails"
	nicoMachineIDLabel        = "infra.nvidia.com/machine-id"
	nicoSiteIDLabel           = "infra.nvidia.com/site-id"
)

// HardwareDetails mirrors the Metal3 hardware details annotation schema.
type HardwareDetails struct {
	SystemVendor SystemVendor `json:"systemVendor,omitempty"`
	Firmware     Firmware     `json:"firmware,omitempty"`
	RAMMebibytes int          `json:"ramMebibytes,omitempty"`
	NIC          []NIC        `json:"nics,omitempty"`
	CPU          CPU          `json:"cpu,omitempty"`
	Storage      []Disk       `json:"storage,omitempty"`
}

type SystemVendor struct {
	Manufacturer string `json:"manufacturer,omitempty"`
	ProductName  string `json:"productName,omitempty"`
	SerialNumber string `json:"serialNumber,omitempty"`
}

type Firmware struct {
	BIOS BIOSFirmware `json:"bios,omitempty"`
}

type BIOSFirmware struct {
	Version string `json:"version,omitempty"`
	Date    string `json:"date,omitempty"`
}

type NIC struct {
	MAC  string `json:"mac,omitempty"`
	Name string `json:"name,omitempty"`
}

type CPU struct {
	Arch  string `json:"arch,omitempty"`
	Model string `json:"model,omitempty"`
	Count int    `json:"count,omitempty"`
}

type Disk struct {
	SizeBytes int64  `json:"sizeBytes,omitempty"`
	Vendor    string `json:"vendor,omitempty"`
	Model     string `json:"model,omitempty"`
}

// MachineToBaremetalHost converts a NICo Machine to a BareMetalHost CR.
// bootMAC is the boot interface MAC address from Site Explorer
// (endpoint.Report.MachineSetupStatus.EvaluatedBootInterface). When empty,
// falls back to the first NIC in machine metadata as a best-effort heuristic.
func MachineToBaremetalHost(
	m nico.Machine,
	sku *nico.Sku,
	bootMAC string,
	namespace string,
) *metal3.BareMetalHost {
	bmh := &metal3.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bmhName(m),
			Namespace: namespace,
			Labels: map[string]string{
				nicoMachineIDLabel: derefStr(m.Id),
			},
		},
		Spec: metal3.BareMetalHostSpec{
			Online:                true,
			ExternallyProvisioned: true,
		},
	}

	if m.SiteId != nil {
		bmh.Labels[nicoSiteIDLabel] = *m.SiteId
	}

	if m.Status != nil {
		bmh.Spec.Online = machineIsOnline(*m.Status)
	}

	if m.Metadata != nil {
		if m.Metadata.BmcInfo != nil && m.Metadata.BmcInfo.Ip.Get() != nil {
			bmh.Spec.BMC = metal3.BMCDetails{
				Address: fmt.Sprintf("redfish+https://%s/redfish/v1/Systems/1", *m.Metadata.BmcInfo.Ip.Get()),
			}
		}
	}

	// Prefer the boot MAC from Site Explorer (evaluatedBootInterface); fall
	// back to the first NIC in machine metadata when Site Explorer data is
	// unavailable (e.g. 403 on the endpoint, or the machine hasn't been explored yet).
	if bootMAC != "" {
		bmh.Spec.BootMACAddress = bootMAC
	} else if m.Metadata != nil && len(m.Metadata.NetworkInterfaces) > 0 {
		if mac := m.Metadata.NetworkInterfaces[0].MacAddress.Get(); mac != nil {
			bmh.Spec.BootMACAddress = *mac
		}
	}

	hwDetails := buildHardwareDetails(m, sku)
	hwJSON, err := json.Marshal(hwDetails)
	if err == nil {
		if bmh.Annotations == nil {
			bmh.Annotations = map[string]string{}
		}
		bmh.Annotations[hardwareDetailsAnnotation] = string(hwJSON)
	}

	return bmh
}

// FirmwareToHostFirmwareComponents converts firmware version data to a
// HostFirmwareComponents status.
func FirmwareToHostFirmwareComponents(
	machineID string,
	firmwareVersions map[string]string,
	m nico.Machine,
	namespace string,
) *metal3.HostFirmwareComponents {
	hfc := &metal3.HostFirmwareComponents{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bmhNameFromID(machineID),
			Namespace: namespace,
			Labels: map[string]string{
				nicoMachineIDLabel: machineID,
			},
		},
		Status: metal3.HostFirmwareComponentsStatus{
			Components: make([]metal3.FirmwareComponentStatus, 0),
		},
	}

	for component, version := range firmwareVersions {
		hfc.Status.Components = append(hfc.Status.Components, metal3.FirmwareComponentStatus{
			Component:      component,
			InitialVersion: version,
			CurrentVersion: version,
		})
	}

	if m.Metadata != nil {
		if m.Metadata.BmcInfo != nil && m.Metadata.BmcInfo.FirmwareRevision.Get() != nil {
			hfc.Status.Components = append(hfc.Status.Components, metal3.FirmwareComponentStatus{
				Component:      "bmc",
				InitialVersion: *m.Metadata.BmcInfo.FirmwareRevision.Get(),
				CurrentVersion: *m.Metadata.BmcInfo.FirmwareRevision.Get(),
			})
		}
		for i, gpu := range m.Metadata.Gpus {
			if gpu.VbiosVersion.Get() != nil {
				hfc.Status.Components = append(hfc.Status.Components, metal3.FirmwareComponentStatus{
					Component:      fmt.Sprintf("gpu-%d-vbios", i),
					InitialVersion: *gpu.VbiosVersion.Get(),
					CurrentVersion: *gpu.VbiosVersion.Get(),
				})
			}
		}
	}

	return hfc
}

func buildHardwareDetails(m nico.Machine, sku *nico.Sku) HardwareDetails {
	hd := HardwareDetails{}

	if v := m.Vendor.Get(); v != nil {
		hd.SystemVendor.Manufacturer = *v
	}
	if v := m.ProductName.Get(); v != nil {
		hd.SystemVendor.ProductName = *v
	}
	if v := m.SerialNumber.Get(); v != nil {
		hd.SystemVendor.SerialNumber = *v
	}

	if m.ControllerMachineType.Get() != nil {
		hd.CPU.Arch = *m.ControllerMachineType.Get()
	}

	if m.Metadata != nil {
		if m.Metadata.DmiData != nil {
			if v := m.Metadata.DmiData.BiosVersion.Get(); v != nil {
				hd.Firmware.BIOS.Version = *v
			}
			if v := m.Metadata.DmiData.BiosDate.Get(); v != nil {
				hd.Firmware.BIOS.Date = *v
			}
		}
		for _, nic := range m.Metadata.NetworkInterfaces {
			n := NIC{}
			if mac := nic.MacAddress.Get(); mac != nil {
				n.MAC = *mac
			}
			if dev := nic.Device.Get(); dev != nil {
				n.Name = *dev
			}
			hd.NIC = append(hd.NIC, n)
		}
	}

	if sku != nil && sku.Components != nil {
		for _, cpu := range sku.Components.Cpus {
			if cpu.Model != nil {
				hd.CPU.Model = *cpu.Model
			}
			if cpu.Count != nil {
				hd.CPU.Count = int(*cpu.Count)
			}
		}
		for _, mem := range sku.Components.Memory {
			if mem.CapacityMb != nil && mem.Count != nil {
				// Convert MB to MiB: 1 MB = 1,000,000 / 1,048,576 MiB ≈ 0.9537 MiB
				totalMB := int64(*mem.CapacityMb) * int64(*mem.Count)
				hd.RAMMebibytes += int(totalMB * 1000000 / 1048576)
			}
		}
		for _, stor := range sku.Components.Storage {
			d := Disk{}
			if stor.Vendor != nil {
				d.Vendor = *stor.Vendor
			}
			if stor.Model != nil {
				d.Model = *stor.Model
			}
			if stor.CapacityMb != nil {
				d.SizeBytes = int64(*stor.CapacityMb) * 1000000
			}
			hd.Storage = append(hd.Storage, d)
		}
	}

	return hd
}

func machineIsOnline(status nico.MachineStatus) bool {
	switch status {
	case nico.MACHINESTATUS_READY, nico.MACHINESTATUS_IN_USE:
		return true
	default:
		return false
	}
}

func bmhName(m nico.Machine) string {
	return bmhNameFromID(derefStr(m.Id))
}

func bmhNameFromID(id string) string {
	return fmt.Sprintf("nico-%s", id)
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
