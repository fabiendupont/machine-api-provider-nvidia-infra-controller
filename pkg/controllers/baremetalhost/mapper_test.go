package baremetalhost

import (
	"encoding/json"
	"testing"

	nico "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

func ptr[T any](v T) *T { return &v }

func TestMachineToBaremetalHost_BasicFields(t *testing.T) {
	m := nico.Machine{
		Id:     ptr("machine-1"),
		SiteId: ptr("site-1"),
		Status: ptr(nico.MACHINESTATUS_READY),
	}
	m.Vendor = *nico.NewNullableString(ptr("NVIDIA"))
	m.ProductName = *nico.NewNullableString(ptr("DGX H100"))
	m.SerialNumber = *nico.NewNullableString(ptr("SN-123"))

	bmh := MachineToBaremetalHost(m, nil, "", "test-ns")

	if bmh.Name != "nico-machine-1" {
		t.Errorf("Name = %s, want nico-machine-1", bmh.Name)
	}
	if bmh.Namespace != "test-ns" {
		t.Errorf("Namespace = %s, want test-ns", bmh.Namespace)
	}
	if bmh.Labels[nicoMachineIDLabel] != "machine-1" {
		t.Errorf("machineId label = %s", bmh.Labels[nicoMachineIDLabel])
	}
	if bmh.Labels[nicoSiteIDLabel] != "site-1" {
		t.Errorf("siteId label = %s", bmh.Labels[nicoSiteIDLabel])
	}
	if !bmh.Spec.Online {
		t.Error("Expected online=true for Ready machine")
	}
	if !bmh.Spec.ExternallyProvisioned {
		t.Error("Expected externallyProvisioned=true")
	}
}

func TestMachineToBaremetalHost_BMCAndMAC(t *testing.T) {
	m := nico.Machine{
		Id:     ptr("machine-1"),
		Status: ptr(nico.MACHINESTATUS_IN_USE),
		Metadata: &nico.MachineMetadata{
			BmcInfo: &nico.MachineBMCInfo{
				Ip: *nico.NewNullableString(ptr("10.0.0.100")),
			},
			NetworkInterfaces: []nico.MachineNetworkInterface{
				{MacAddress: *nico.NewNullableString(ptr("aa:bb:cc:dd:ee:ff"))},
			},
		},
	}

	// No bootMAC from Site Explorer — should fall back to first NIC
	bmh := MachineToBaremetalHost(m, nil, "", "test-ns")

	if bmh.Spec.BMC.Address != "redfish+https://10.0.0.100/redfish/v1/Systems/1" {
		t.Errorf("BMC address = %s", bmh.Spec.BMC.Address)
	}
	if bmh.Spec.BootMACAddress != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("BootMACAddress (fallback) = %s, want aa:bb:cc:dd:ee:ff", bmh.Spec.BootMACAddress)
	}
}

func TestMachineToBaremetalHost_BootMACFromSiteExplorer(t *testing.T) {
	m := nico.Machine{
		Id:     ptr("machine-1"),
		Status: ptr(nico.MACHINESTATUS_READY),
		Metadata: &nico.MachineMetadata{
			NetworkInterfaces: []nico.MachineNetworkInterface{
				// First NIC — should NOT be used when Site Explorer provides a boot MAC
				{MacAddress: *nico.NewNullableString(ptr("aa:bb:cc:dd:ee:ff"))},
			},
		},
	}

	siteExplorerBootMAC := "11:22:33:44:55:66"
	bmh := MachineToBaremetalHost(m, nil, siteExplorerBootMAC, "test-ns")

	if bmh.Spec.BootMACAddress != siteExplorerBootMAC {
		t.Errorf("BootMACAddress = %s, want %s (Site Explorer)", bmh.Spec.BootMACAddress, siteExplorerBootMAC)
	}
}

func TestMachineToBaremetalHost_Offline(t *testing.T) {
	m := nico.Machine{
		Id:     ptr("machine-1"),
		Status: ptr(nico.MACHINESTATUS_MAINTENANCE),
	}

	bmh := MachineToBaremetalHost(m, nil, "", "test-ns")
	if bmh.Spec.Online {
		t.Error("Expected online=false for Maintenance machine")
	}
}

func TestMachineToBaremetalHost_WithSKU(t *testing.T) {
	m := nico.Machine{
		Id: ptr("machine-1"),
	}
	m.Vendor = *nico.NewNullableString(ptr("NVIDIA"))

	sku := &nico.Sku{
		Components: &nico.SkuComponents{
			Cpus: []nico.SkuCpu{
				{Model: ptr("AMD EPYC 9654"), Count: ptr(uint32(2)), ThreadCount: ptr(uint32(192))},
			},
			Memory: []nico.SkuMemory{
				{CapacityMb: ptr(uint32(32768)), Count: ptr(uint32(32)), MemoryType: ptr("DDR5")},
			},
			Storage: []nico.SkuStorage{
				{Vendor: ptr("Samsung"), Model: ptr("PM9A3"), CapacityMb: ptr(uint32(3840000))},
			},
		},
	}

	bmh := MachineToBaremetalHost(m, sku, "", "test-ns")

	var hd HardwareDetails
	err := json.Unmarshal([]byte(bmh.Annotations[hardwareDetailsAnnotation]), &hd)
	if err != nil {
		t.Fatalf("Failed to parse hardware details: %v", err)
	}
	if hd.CPU.Model != "AMD EPYC 9654" {
		t.Errorf("CPU model = %s", hd.CPU.Model)
	}
	if hd.CPU.Count != 2 {
		t.Errorf("CPU count = %d", hd.CPU.Count)
	}
	// 32768 MB * 32 modules = 1048576 MB → converted to MiB: 1048576 * 1000000 / 1048576 = 1000000 MiB
	expectedRAM := int(int64(32768) * int64(32) * 1000000 / 1048576)
	if hd.RAMMebibytes != expectedRAM {
		t.Errorf("RAM = %d MiB, want %d", hd.RAMMebibytes, expectedRAM)
	}
	if len(hd.Storage) != 1 || hd.Storage[0].Vendor != "Samsung" {
		t.Errorf("Storage = %+v", hd.Storage)
	}
	// 3840000 MB → bytes
	expectedBytes := int64(3840000) * 1000000
	if hd.Storage[0].SizeBytes != expectedBytes {
		t.Errorf("Storage size = %d bytes, want %d", hd.Storage[0].SizeBytes, expectedBytes)
	}
}

func TestMachineToBaremetalHost_DMIData(t *testing.T) {
	m := nico.Machine{
		Id: ptr("machine-1"),
		Metadata: &nico.MachineMetadata{
			DmiData: &nico.MachineDMIData{
				BiosVersion: *nico.NewNullableString(ptr("1.8.0")),
				BiosDate:    *nico.NewNullableString(ptr("2024-06-15")),
			},
		},
	}

	bmh := MachineToBaremetalHost(m, nil, "", "test-ns")

	var hd HardwareDetails
	_ = json.Unmarshal([]byte(bmh.Annotations[hardwareDetailsAnnotation]), &hd)
	if hd.Firmware.BIOS.Version != "1.8.0" {
		t.Errorf("BIOS version = %s", hd.Firmware.BIOS.Version)
	}
	if hd.Firmware.BIOS.Date != "2024-06-15" {
		t.Errorf("BIOS date = %s", hd.Firmware.BIOS.Date)
	}
}

func TestFirmwareToHostFirmwareComponents(t *testing.T) {
	fw := map[string]string{
		"BMC":  "1.2.3",
		"UEFI": "4.5.6",
	}
	m := nico.Machine{
		Id: ptr("machine-1"),
		Metadata: &nico.MachineMetadata{
			BmcInfo: &nico.MachineBMCInfo{
				FirmwareRevision: *nico.NewNullableString(ptr("7.8.9")),
			},
			Gpus: []nico.MachineGPUInfo{
				{VbiosVersion: *nico.NewNullableString(ptr("96.00.89.00.01"))},
			},
		},
	}

	hfc := FirmwareToHostFirmwareComponents("machine-1", fw, m, "test-ns")

	if hfc.Name != "nico-machine-1" {
		t.Errorf("Name = %s", hfc.Name)
	}
	// 2 from firmwareVersions + 1 BMC + 1 GPU vbios = 4
	if len(hfc.Status.Components) != 4 {
		t.Errorf("Components count = %d, want 4", len(hfc.Status.Components))
	}

	componentMap := map[string]string{}
	for _, c := range hfc.Status.Components {
		componentMap[c.Component] = c.CurrentVersion
	}
	if componentMap["BMC"] != "1.2.3" {
		t.Errorf("BMC version = %s", componentMap["BMC"])
	}
	if componentMap["UEFI"] != "4.5.6" {
		t.Errorf("UEFI version = %s", componentMap["UEFI"])
	}
	if componentMap["bmc"] != "7.8.9" {
		t.Errorf("bmc firmware revision = %s", componentMap["bmc"])
	}
	if componentMap["gpu-0-vbios"] != "96.00.89.00.01" {
		t.Errorf("gpu-0-vbios = %s", componentMap["gpu-0-vbios"])
	}
}

func TestMachineIsOnline(t *testing.T) {
	tests := []struct {
		status nico.MachineStatus
		online bool
	}{
		{nico.MACHINESTATUS_READY, true},
		{nico.MACHINESTATUS_IN_USE, true},
		{nico.MACHINESTATUS_MAINTENANCE, false},
		{nico.MACHINESTATUS_ERROR, false},
		{nico.MACHINESTATUS_INITIALIZING, false},
		{nico.MACHINESTATUS_DECOMMISSIONED, false},
	}

	for _, tt := range tests {
		if got := machineIsOnline(tt.status); got != tt.online {
			t.Errorf("machineIsOnline(%s) = %v, want %v", tt.status, got, tt.online)
		}
	}
}
