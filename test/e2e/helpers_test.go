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

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	keycloakRealm        = "nico-dev"
	keycloakClientID     = "nico-api"
	keycloakClientSecret = "nico-local-secret"
	keycloakUsername      = "admin@example.com"
	keycloakPassword     = "adminpassword"
)

// getKeycloakToken acquires a JWT from Keycloak using the resource owner password grant.
func getKeycloakToken() string {
	keycloakURL := os.Getenv("NVIDIA_CARBIDE_KEYCLOAK_URL")
	Expect(keycloakURL).NotTo(BeEmpty(), "NVIDIA_CARBIDE_KEYCLOAK_URL must be set")

	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", keycloakURL, keycloakRealm)

	data := url.Values{
		"grant_type":    {"password"},
		"client_id":     {keycloakClientID},
		"client_secret": {keycloakClientSecret},
		"username":      {keycloakUsername},
		"password":      {keycloakPassword},
	}

	resp, err := http.Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	Expect(err).NotTo(HaveOccurred(), "Failed to request Keycloak token")
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusOK),
		"Keycloak token request failed with status %d: %s", resp.StatusCode, string(body))

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	Expect(json.Unmarshal(body, &tokenResp)).To(Succeed())
	Expect(tokenResp.AccessToken).NotTo(BeEmpty(), "Received empty access token from Keycloak")

	_, _ = fmt.Fprintf(GinkgoWriter, "Successfully acquired Keycloak token\n")
	return tokenResp.AccessToken
}

// createCredentialsSecret creates a Kubernetes secret with NICo API credentials.
func createCredentialsSecret(ctx context.Context, k8sClient client.Client, name, namespace, token string) *corev1.Secret {
	// Use the in-cluster endpoint if available (for controllers running inside the cluster),
	// otherwise fall back to the external endpoint.
	endpoint := os.Getenv("NVIDIA_CARBIDE_API_ENDPOINT_INTERNAL")
	if endpoint == "" {
		endpoint = os.Getenv("NVIDIA_CARBIDE_API_ENDPOINT")
	}
	Expect(endpoint).NotTo(BeEmpty(), "NVIDIA_CARBIDE_API_ENDPOINT or NVIDIA_CARBIDE_API_ENDPOINT_INTERNAL must be set")

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"endpoint": []byte(endpoint),
			"orgName":  []byte("test-org"),
			"token":    []byte(token),
		},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	_, _ = fmt.Fprintf(GinkgoWriter, "Created credentials secret %s/%s\n", namespace, name)
	return secret
}

// nicoAPIRequest makes an authenticated request to the NICo REST API.
func nicoAPIRequest(method, path, token string, body interface{}) (map[string]interface{}, int) {
	endpoint := os.Getenv("NVIDIA_CARBIDE_API_ENDPOINT")
	Expect(endpoint).NotTo(BeEmpty())

	var reqBody io.Reader
	if body != nil {
		jsonBytes, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		reqBody = bytes.NewReader(jsonBytes)
	}

	req, err := http.NewRequest(method, endpoint+path, reqBody)
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())

	var result map[string]interface{}
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &result)
	}

	_, _ = fmt.Fprintf(GinkgoWriter, "%s %s -> %d\n", method, path, resp.StatusCode)
	return result, resp.StatusCode
}

// createVPCViaAPI creates a VPC via the NICo REST API and returns its ID.
func createVPCViaAPI(token, orgName, siteID, name string) string {
	body := map[string]interface{}{
		"name":   name,
		"siteId": siteID,
		// Subnets can only be created on Ethernet VPCs (see subnet handler:
		// "VPC ... must have Ethernet network virtualization type"). When the
		// type is omitted the site defaults it to FNN (native networking is
		// enabled on local-dev-site), so request it explicitly.
		"networkVirtualizationType": "ETHERNET_VIRTUALIZER",
	}
	result, status := nicoAPIRequest("POST", fmt.Sprintf("/v2/org/%s/nico/vpc", orgName), token, body)
	Expect(status).To(Equal(http.StatusCreated), "Failed to create VPC: %v", result)
	vpcID, ok := result["id"].(string)
	Expect(ok).To(BeTrue(), "VPC response missing id")
	_, _ = fmt.Fprintf(GinkgoWriter, "Created VPC %s (id=%s)\n", name, vpcID)
	return vpcID
}

// createIPBlockViaAPI creates an IP block via the NICo REST API and returns its ID.
func createIPBlockViaAPI(token, orgName, siteID, name string) string {
	body := map[string]interface{}{
		"name":            name,
		"siteId":          siteID,
		"prefix":          "10.0.0.0",
		"prefixLength":    16,
		"protocolVersion": "IPv4",
		"routingType":     "DatacenterOnly",
	}
	result, status := nicoAPIRequest("POST", fmt.Sprintf("/v2/org/%s/nico/ipblock", orgName), token, body)
	Expect(status).To(Equal(http.StatusCreated), "Failed to create IP block: %v", result)
	ipBlockID, ok := result["id"].(string)
	Expect(ok).To(BeTrue(), "IP block response missing id")
	_, _ = fmt.Fprintf(GinkgoWriter, "Created IP block %s (id=%s)\n", name, ipBlockID)
	return ipBlockID
}

// createSubnetViaAPI creates a subnet via the NICo REST API and returns its ID.
func createSubnetViaAPI(token, orgName, vpcID, ipBlockID, name string) string {
	body := map[string]interface{}{
		"name":         name,
		"vpcId":        vpcID,
		"ipv4BlockId":  ipBlockID,
		"prefixLength": 24,
	}
	result, status := nicoAPIRequest("POST", fmt.Sprintf("/v2/org/%s/nico/subnet", orgName), token, body)
	Expect(status).To(Equal(http.StatusCreated), "Failed to create subnet: %v", result)
	subnetID, ok := result["id"].(string)
	Expect(ok).To(BeTrue(), "Subnet response missing id")
	_, _ = fmt.Fprintf(GinkgoWriter, "Created subnet %s (id=%s)\n", name, subnetID)
	return subnetID
}

// getExistingSiteID finds the local-dev-site created by setup-local.sh.
func getExistingSiteID(token, orgName string) string {
	endpoint := os.Getenv("NVIDIA_CARBIDE_API_ENDPOINT")
	apiBase := fmt.Sprintf("/v2/org/%s/nico", orgName)

	req, err := http.NewRequest("GET", endpoint+apiBase+"/site", nil)
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())

	var sites []map[string]interface{}
	Expect(json.Unmarshal(body, &sites)).To(Succeed())
	Expect(sites).NotTo(BeEmpty(), "No sites found — was setup-local.sh run?")

	siteID := sites[0]["id"].(string)
	siteName := sites[0]["name"].(string)
	_, _ = fmt.Fprintf(GinkgoWriter, "Using existing site %s (id=%s)\n", siteName, siteID)
	return siteID
}

// ensureSiteRegistered ensures the site is in Registered state.
func ensureSiteRegistered(siteID string) {
	cmd := exec.Command("kubectl", "exec", "-n", "postgres", "statefulset/postgres", "--",
		"psql", "-U", "nico", "-d", "nico", "-c",
		fmt.Sprintf("UPDATE site SET status = 'Registered' WHERE id = '%s' AND status != 'Registered'", siteID))
	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "Failed to ensure site is registered: %s", string(output))
	_, _ = fmt.Fprintf(GinkgoWriter, "Ensured site %s is Registered\n", siteID)
}

// listTenantAccounts returns the tenant accounts for the org (the list endpoint
// returns a bare JSON array, so it can't go through nicoAPIRequest).
func listTenantAccounts(token, orgName string) []map[string]interface{} {
	endpoint := os.Getenv("NVIDIA_CARBIDE_API_ENDPOINT")
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/v2/org/%s/nico/tenant/account", endpoint, orgName), nil)
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	var accounts []map[string]interface{}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &accounts)
	}
	return accounts
}

// ensureTargetedInstanceCreationAccount ensures a Ready TenantAccount exists with
// the TargetedInstanceCreation capability enabled. The controller checks this via
// the TenantAccount API (GetAllTenantAccount -> SiteCapabilities); when that call
// succeeds it does NOT fall back to the deprecated tenant.config flag, so the
// account must exist and be Ready. The capability validation requires exactly one
// entry with empty siteIds (the global default), which the controller treats as
// "applies to all sites". Accounts are created "Invited" ("pending accept") and
// there is no API to accept them in the mock stack, so force Ready in the DB like
// the other status hacks.
func ensureTargetedInstanceCreationAccount(token, orgName, infraProviderID string) {
	apiBase := fmt.Sprintf("/v2/org/%s/nico", orgName)

	// Reuse an existing account or create one (create is not idempotent).
	var accountID string
	if accounts := listTenantAccounts(token, orgName); len(accounts) > 0 {
		accountID = accounts[0]["id"].(string)
	} else {
		result, status := nicoAPIRequest("POST", apiBase+"/tenant/account", token, map[string]interface{}{
			"tenantOrg":                orgName,
			"infrastructureProviderId": infraProviderID,
		})
		Expect(status).To(Equal(http.StatusCreated), "Failed to create tenant account: %v", result)
		accountID = result["id"].(string)
	}

	// Enable targeted instance creation globally (single empty-siteIds entry).
	result, status := nicoAPIRequest("PATCH", fmt.Sprintf("%s/tenant/account/%s", apiBase, accountID), token, map[string]interface{}{
		"siteCapabilities": []map[string]interface{}{
			{"siteIds": []string{}, "targetedInstanceCreation": true},
		},
	})
	Expect(status).To(Equal(http.StatusOK), "Failed to set tenant account capabilities: %v", result)

	// Force Ready (no API to accept the account in the mock stack).
	cmd := exec.Command("kubectl", "exec", "-n", "postgres", "statefulset/postgres", "--",
		"psql", "-U", "nico", "-d", "nico", "-c",
		fmt.Sprintf("UPDATE tenant_account SET status = 'Ready' WHERE id = '%s' AND status != 'Ready'", accountID))
	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "Failed to ensure tenant account is ready: %s", string(output))
	_, _ = fmt.Fprintf(GinkgoWriter, "Ensured tenant account %s is Ready with targeted instance creation\n", accountID)
}

// ensureVPCReady ensures the VPC is in Ready state. mock-core completes the
// create workflow but never advances the VPC out of Provisioning, and the
// subnet handler requires the VPC to be Ready ("VPC ... must be in Ready state
// in order to create Subnet"), so force it directly like the other status hacks.
func ensureVPCReady(vpcID string) {
	cmd := exec.Command("kubectl", "exec", "-n", "postgres", "statefulset/postgres", "--",
		"psql", "-U", "nico", "-d", "nico", "-c",
		fmt.Sprintf("UPDATE vpc SET status = 'Ready' WHERE id = '%s' AND status != 'Ready'", vpcID))
	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "Failed to ensure VPC is ready: %s", string(output))
	_, _ = fmt.Fprintf(GinkgoWriter, "Ensured VPC %s is Ready\n", vpcID)
}

// ensureSubnetReady ensures the subnet is in Ready state.
func ensureSubnetReady(subnetID string) {
	cmd := exec.Command("kubectl", "exec", "-n", "postgres", "statefulset/postgres", "--",
		"psql", "-U", "nico", "-d", "nico", "-c",
		fmt.Sprintf("UPDATE subnet SET status = 'Ready' WHERE id = '%s' AND status != 'Ready'", subnetID))
	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "Failed to ensure subnet is ready: %s", string(output))
	_, _ = fmt.Fprintf(GinkgoWriter, "Ensured subnet %s is Ready\n", subnetID)
}

// getInfraProviderID retrieves the infrastructure provider ID for the org.
func getInfraProviderID(token, orgName string) string {
	apiBase := fmt.Sprintf("/v2/org/%s/nico", orgName)
	result, status := nicoAPIRequest("GET", apiBase+"/infrastructure-provider/current", token, nil)
	Expect(status).To(Equal(http.StatusOK), "Failed to get infrastructure provider: %v", result)
	id := result["id"].(string)
	_, _ = fmt.Fprintf(GinkgoWriter, "Infrastructure Provider ID: %s\n", id)
	return id
}

// createTestMachineInDB inserts a test machine directly into PostgreSQL.
// The mock-core doesn't persist machines, so we create one via DB for the
// controller to use with machineId (bypassing instanceTypeId).
func createTestMachineInDB(siteID, infraProviderID, machineID string) {
	sql := fmt.Sprintf(
		"INSERT INTO machine (id, infrastructure_provider_id, site_id, controller_machine_id, status, is_in_maintenance, is_usable_by_tenant, is_network_degraded, is_assigned, is_missing_on_site, created, updated) "+
			"VALUES ('%s', '%s', '%s', '%s', 'Ready', false, true, false, false, false, NOW(), NOW()) ON CONFLICT (id) DO NOTHING",
		machineID, infraProviderID, siteID, machineID)
	cmd := exec.Command("kubectl", "exec", "-n", "postgres", "statefulset/postgres", "--",
		"psql", "-U", "nico", "-d", "nico", "-c", sql)
	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "Failed to create test machine in DB: %s", string(output))
	_, _ = fmt.Fprintf(GinkgoWriter, "Created test machine %s in DB\n", machineID)
}

// setupInfrastructureViaAPI uses the existing local-dev-site and creates
// Tenant -> IP Block -> Allocation -> VPC -> Subnet + test machine in DB.
// Returns siteID, tenantID, vpcID, subnetID, machineID for use in tests.
func setupInfrastructureViaAPI(token, orgName, prefix string) (siteID, tenantID, vpcID, subnetID, machineID string) {
	apiBase := fmt.Sprintf("/v2/org/%s/nico", orgName)

	// Use the existing site (has a connected site-agent for Temporal workflows)
	siteID = getExistingSiteID(token, orgName)
	ensureSiteRegistered(siteID)

	// Get or create Tenant (idempotent)
	nicoAPIRequest("POST", apiBase+"/tenant", token, map[string]interface{}{"org": orgName})
	currentTenant, tStatus := nicoAPIRequest("GET", apiBase+"/tenant/current", token, nil)
	Expect(tStatus).To(Equal(http.StatusOK), "Failed to get current tenant: %v", currentTenant)
	tenantID = currentTenant["id"].(string)
	_, _ = fmt.Fprintf(GinkgoWriter, "Tenant ID: %s\n", tenantID)

	// Enable targeted instance creation via the TenantAccount API. The controller
	// checks the tenant account's SiteCapabilities, not the deprecated tenant config.
	infraProviderID := getInfraProviderID(token, orgName)
	ensureTargetedInstanceCreationAccount(token, orgName, infraProviderID)

	// Create IP Block
	ipBlockID := createIPBlockViaAPI(token, orgName, siteID, prefix+"-ipblock")

	// Create Allocation for IPBlock
	allocResult, status := nicoAPIRequest("POST", apiBase+"/allocation", token, map[string]interface{}{
		"name":     prefix + "-allocation",
		"tenantId": tenantID,
		"siteId":   siteID,
		"allocationConstraints": []map[string]interface{}{
			{"resourceType": "IPBlock", "resourceTypeId": ipBlockID, "constraintType": "OnDemand", "constraintValue": 24},
		},
	})
	Expect(status).To(Equal(http.StatusCreated), "Failed to create allocation: %v", allocResult)

	// Extract the child IP block ID from the allocation response
	constraints := allocResult["allocationConstraints"].([]interface{})
	firstConstraint := constraints[0].(map[string]interface{})
	childIPBlockID := firstConstraint["derivedResourceId"].(string)
	_, _ = fmt.Fprintf(GinkgoWriter, "Child IP Block ID: %s\n", childIPBlockID)

	// Create VPC
	vpcID = createVPCViaAPI(token, orgName, siteID, prefix+"-vpc")
	ensureVPCReady(vpcID)

	// Create Subnet (uses child IP block, not the parent)
	subnetID = createSubnetViaAPI(token, orgName, vpcID, childIPBlockID, prefix+"-subnet")
	ensureSubnetReady(subnetID)

	// Create a test machine in DB (mock-core doesn't persist machines,
	// so we insert one directly for the controller to use with machineId)
	machineID = prefix + "-machine"
	createTestMachineInDB(siteID, infraProviderID, machineID)

	return siteID, tenantID, vpcID, subnetID, machineID
}

// cleanupInfrastructureViaAPI deletes the infrastructure created by setupInfrastructureViaAPI.
func cleanupInfrastructureViaAPI(token, orgName, subnetID, vpcID, siteID string) {
	if subnetID != "" {
		nicoAPIRequest("DELETE", fmt.Sprintf("/v2/org/%s/nico/subnet/%s", orgName, subnetID), token, nil)
	}
	if vpcID != "" {
		nicoAPIRequest("DELETE", fmt.Sprintf("/v2/org/%s/nico/vpc/%s", orgName, vpcID), token, nil)
	}
	if siteID != "" {
		nicoAPIRequest("DELETE", fmt.Sprintf("/v2/org/%s/nico/site/%s", orgName, siteID), token, nil)
	}
}
