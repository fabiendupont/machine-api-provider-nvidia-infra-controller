package baremetalhost

import (
	"context"
	"fmt"
	"sync"
	"time"

	metal3 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	nico "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
	"github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/actuators/machine"
)

const (
	syncInterval = 60 * time.Second
	skuCacheTTL  = 5 * time.Minute
)

// BMHSyncConfig controls how BareMetalHost CRs are created by the sync controller.
type BMHSyncConfig struct {
	// ExternallyProvisioned sets Spec.ExternallyProvisioned on created BMH CRs.
	// When true (default), NICo owns provisioning and BMO acts as inventory only.
	// When false, BMO/Ironic drives provisioning and BMCCredentialsSecretTemplate
	// must name a Secret (or ESO-managed Secret) containing BMC username/password.
	ExternallyProvisioned bool

	// BMCCredentialsSecretTemplate is a fmt format string with one %s placeholder
	// that is substituted with the NICo machine ID to produce the per-machine
	// Kubernetes Secret name holding BMC credentials (e.g. "bmc-%s").
	// Only used when ExternallyProvisioned is false.
	BMCCredentialsSecretTemplate string
}

// Reconciler syncs NICo machines to BareMetalHost and
// HostFirmwareComponents CRs.
type Reconciler struct {
	client.Client
	NicoClient machine.NicoClientInterface
	OrgName    string
	Namespace  string
	Config     BMHSyncConfig
	ESOConfig  ESOSyncConfig

	skuCache       map[string]*nico.Sku
	skuCacheExpiry time.Time
	skuMu          sync.Mutex
}

func (r *Reconciler) syncMachine(
	ctx context.Context,
	m nico.Machine,
	skuMap map[string]*nico.Sku,
	seData *siteExplorerData,
) error {
	machineID := derefStr(m.Id)
	sku := skuMap[machineID]

	// Resolve boot MAC: prefer machineId-keyed lookup (when NICo populates it),
	// fall back to BMC IP keyed lookup (endpoint.address always present).
	var bootMAC string
	if seData != nil {
		if mac, ok := seData.BootMACs[machineID]; ok {
			bootMAC = mac
		} else if m.Metadata != nil && m.Metadata.BmcInfo != nil {
			if ip := m.Metadata.BmcInfo.Ip.Get(); ip != nil {
				bootMAC = seData.BootMACsByBMCIP[*ip]
			}
		}
	}

	desired := MachineToBaremetalHost(m, sku, bootMAC, r.Namespace, r.Config)

	existing := &metal3.BareMetalHost{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if errors.IsNotFound(err) {
		if createErr := r.Create(ctx, desired); createErr != nil {
			return fmt.Errorf("create BMH: %w", createErr)
		}
	} else if err != nil {
		return fmt.Errorf("get BMH: %w", err)
	} else {
		existing.Spec = desired.Spec
		existing.Labels = desired.Labels
		existing.Annotations = desired.Annotations
		if updateErr := r.Update(ctx, existing); updateErr != nil {
			return fmt.Errorf("update BMH: %w", updateErr)
		}
	}

	if !r.Config.ExternallyProvisioned {
		if err := r.syncExternalSecret(ctx, machineID); err != nil {
			return fmt.Errorf("sync ExternalSecret: %w", err)
		}
	}

	if seData != nil {
		if fw, ok := seData.FirmwareVersions[machineID]; ok {
			if err := r.syncFirmwareComponents(ctx, machineID, fw, m); err != nil {
				return fmt.Errorf("sync HFC: %w", err)
			}
		}
	}

	return nil
}

func (r *Reconciler) syncFirmwareComponents(
	ctx context.Context,
	machineID string,
	firmwareVersions map[string]string,
	m nico.Machine,
) error {
	desired := FirmwareToHostFirmwareComponents(machineID, firmwareVersions, m, r.Namespace)

	existing := &metal3.HostFirmwareComponents{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if errors.IsNotFound(err) {
		if createErr := r.Create(ctx, desired); createErr != nil {
			return createErr
		}
		existing = desired
	} else if err != nil {
		return err
	}

	existing.Status.Components = desired.Status.Components
	existing.Status.LastUpdated = &metav1.Time{Time: time.Now()}
	return r.Status().Update(ctx, existing)
}

func (r *Reconciler) getSkuMap(ctx context.Context) map[string]*nico.Sku {
	r.skuMu.Lock()
	defer r.skuMu.Unlock()

	if r.skuCache != nil && time.Now().Before(r.skuCacheExpiry) {
		return r.skuCache
	}

	skus, httpResp, err := r.NicoClient.GetAllSku(ctx, r.OrgName)
	if err != nil || httpResp == nil || httpResp.StatusCode >= 300 {
		return nil
	}

	skuMap := make(map[string]*nico.Sku)
	for i := range skus {
		for _, mid := range skus[i].AssociatedMachineIds {
			skuMap[mid] = &skus[i]
		}
	}

	r.skuCache = skuMap
	r.skuCacheExpiry = time.Now().Add(skuCacheTTL)
	return skuMap
}

// siteExplorerData holds per-machine data extracted from a single
// GetAllSiteExplorerEndpoint call to avoid fetching the endpoint list twice.
type siteExplorerData struct {
	FirmwareVersions map[string]map[string]string // machineID → component → version
	BootMACs         map[string]string            // machineID → boot MAC address
	BootMACsByBMCIP  map[string]string            // bmcIP → boot MAC address (fallback when machineID is null)
}

func (r *Reconciler) getSiteExplorerData(ctx context.Context) *siteExplorerData {
	endpoints, httpResp, err := r.NicoClient.GetAllSiteExplorerEndpoint(ctx, r.OrgName)
	if err != nil || httpResp == nil || httpResp.StatusCode >= 300 {
		return nil
	}

	data := &siteExplorerData{
		FirmwareVersions: make(map[string]map[string]string),
		BootMACs:         make(map[string]string),
		BootMACsByBMCIP:  make(map[string]string),
	}
	for _, ep := range endpoints {
		if ep.Report == nil {
			continue
		}

		// Extract boot MAC from machineSetupStatus.evaluatedBootInterface.
		// Prefer the full pair (MAC + Redfish interface ID); fall back to macOnly.
		var bootMAC string
		if setup := ep.Report.MachineSetupStatus; setup != nil {
			if bi := setup.EvaluatedBootInterface; bi != nil {
				if bi.Pair != nil && bi.Pair.MacAddress != "" {
					bootMAC = bi.Pair.MacAddress
				} else if mac := bi.GetMacOnly(); mac != "" {
					bootMAC = mac
				}
			}
		}

		// Always index by BMC IP (endpoint.address) so callers can look up by
		// BMC IP when machineId is null (NICo does not always populate it).
		if ep.Address != "" && bootMAC != "" {
			data.BootMACsByBMCIP[ep.Address] = bootMAC
		}

		// Also index by machineId when present.
		mid := ep.Report.MachineId.Get()
		if mid == nil || *mid == "" {
			continue
		}
		if bootMAC != "" {
			data.BootMACs[*mid] = bootMAC
		}
		if len(ep.Report.FirmwareVersions) > 0 {
			data.FirmwareVersions[*mid] = ep.Report.FirmwareVersions
		}
	}
	return data
}

// SetupWithManager registers the reconciler as a periodic runnable
// since we poll NICo rather than watching CRs.
func SetupWithManager(
	mgr ctrl.Manager,
	nicoClient machine.NicoClientInterface,
	orgName, namespace string,
	cfg BMHSyncConfig,
	esoCfg ESOSyncConfig,
) error {
	r := &Reconciler{
		Client:     mgr.GetClient(),
		NicoClient: nicoClient,
		OrgName:    orgName,
		Namespace:  namespace,
		Config:     cfg,
		ESOConfig:  esoCfg,
	}

	return mgr.Add(r)
}

// Start implements manager.Runnable for periodic polling.
func (r *Reconciler) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("baremetalhost-sync")
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()

	// Run immediately on startup, then on ticker
	for {
		r.sync(log.IntoContext(ctx, logger))
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Reconciler) sync(ctx context.Context) {
	logger := log.FromContext(ctx)

	machines, httpResp, err := r.NicoClient.GetAllMachine(ctx, r.OrgName)
	if err != nil || httpResp == nil || httpResp.StatusCode >= 300 {
		if httpResp != nil && httpResp.StatusCode == 403 {
			logger.Info("Provider-admin access required, skipping BMH sync")
			return
		}
		if err != nil {
			logger.Error(err, "Failed to list NICo machines")
		} else {
			logger.Info("Failed to list NICo machines",
				"statusCode", httpResp.StatusCode)
		}
		return
	}

	skuMap := r.getSkuMap(ctx)
	seData := r.getSiteExplorerData(ctx)

	for _, m := range machines {
		if m.Id == nil {
			continue
		}
		if syncErr := r.syncMachine(ctx, m, skuMap, seData); syncErr != nil {
			logger.Error(syncErr, "Failed to sync machine", "machineId", *m.Id)
		}
	}
}
