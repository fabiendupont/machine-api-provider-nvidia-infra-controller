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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1beta1 "github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/apis/nicoprovider/v1beta1"
)

const (
	testOrgName = "test-org"

	machineCreationTimeout = 5 * time.Minute
	machineDeletionTimeout = 3 * time.Minute
	pollInterval           = 15 * time.Second
)

func TestE2ELive(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting machine-api-provider-nvidia-ncx-infra-controller live e2e test suite\n")
	RunSpecs(t, "Live E2E Suite")
}

var _ = Describe("Live Machine API Provider E2E", Label("live"), func() {
	var (
		ctx           context.Context
		k8sClient     client.Client
		dynamicClient dynamic.Interface
		testNamespace string
		token         string
	)

	BeforeEach(func() {
		ctx = context.Background()

		endpoint := os.Getenv("NVIDIA_CARBIDE_API_ENDPOINT")
		if endpoint == "" {
			Skip("NVIDIA_CARBIDE_API_ENDPOINT must be set")
		}

		kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{},
		)

		config, err := kubeconfig.ClientConfig()
		Expect(err).NotTo(HaveOccurred())

		k8sClient, err = client.New(config, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())

		dynamicClient, err = dynamic.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())

		testNamespace = "default"
		token = getKeycloakToken()
	})

	Context("Machine lifecycle against live NICo API", func() {
		It("should create a Machine, verify provisioning, and delete it", func() {
			machineName := fmt.Sprintf("e2e-live-machine-%d", time.Now().Unix())

			By("Setting up infrastructure via NICo API")
			siteID, tenantID, vpcID, subnetID, machineID := setupInfrastructureViaAPI(token, testOrgName, machineName)

			By("Creating credentials secret")
			secret := createCredentialsSecret(ctx, k8sClient, fmt.Sprintf("%s-creds", machineName), testNamespace, token)

			By("Building provider spec")
			providerSpec := &v1beta1.NicoMachineProviderSpec{
				SiteID:    siteID,
				TenantID:  tenantID,
				VpcID:     vpcID,
				SubnetID:  subnetID,
				MachineID: machineID,
				UserData:  "#cloud-config\nruncmd:\n  - echo e2e-test",
				CredentialsSecret: v1beta1.CredentialsSecretReference{
					Name:      secret.Name,
					Namespace: testNamespace,
				},
				Labels: map[string]string{
					"test":    "e2e-live",
					"machine": machineName,
				},
			}

			providerSpecJSON, err := json.Marshal(providerSpec)
			Expect(err).NotTo(HaveOccurred())

			var providerSpecMap map[string]interface{}
			Expect(json.Unmarshal(providerSpecJSON, &providerSpecMap)).To(Succeed())

			By("Creating a Machine resource")
			machineGVR := schema.GroupVersionResource{
				Group:    "machine.openshift.io",
				Version:  "v1beta1",
				Resource: "machines",
			}

			machine := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "machine.openshift.io/v1beta1",
					"kind":       "Machine",
					"metadata": map[string]interface{}{
						"name":      machineName,
						"namespace": testNamespace,
					},
					"spec": map[string]interface{}{
						"providerSpec": map[string]interface{}{
							"value": providerSpecMap,
						},
					},
				},
			}

			_, err = dynamicClient.Resource(machineGVR).Namespace(testNamespace).Create(ctx, machine, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for machine to be provisioned")
			Eventually(func() string {
				result, err := dynamicClient.Resource(machineGVR).Namespace(testNamespace).Get(ctx, machineName, metav1.GetOptions{})
				if err != nil {
					_, _ = fmt.Fprintf(GinkgoWriter, "Error getting machine: %v\n", err)
					return ""
				}

				instanceID, _, _ := unstructured.NestedString(result.Object, "status", "providerStatus", "instanceId")
				conditions, _, _ := unstructured.NestedSlice(result.Object, "status", "providerStatus", "conditions")
				errorMessage, _, _ := unstructured.NestedString(result.Object, "status", "errorMessage")
				_, _ = fmt.Fprintf(GinkgoWriter, "Machine instanceId=%s errorMessage=%q conditions=%v\n",
					instanceID, errorMessage, conditions)
				return instanceID
			}, machineCreationTimeout, pollInterval).ShouldNot(BeEmpty(), "Machine was not provisioned with an instance ID")

			By("Verifying provider status has addresses")
			result, err := dynamicClient.Resource(machineGVR).Namespace(testNamespace).Get(ctx, machineName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			addresses, _, _ := unstructured.NestedSlice(result.Object, "status", "providerStatus", "addresses")
			_, _ = fmt.Fprintf(GinkgoWriter, "Machine has %d addresses\n", len(addresses))

			By("Verifying provider ID is set")
			providerID, _, _ := unstructured.NestedString(result.Object, "spec", "providerID")
			Expect(providerID).To(HavePrefix("nico://"))

			By("Deleting the machine")
			err = dynamicClient.Resource(machineGVR).Namespace(testNamespace).Delete(ctx, machineName, metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for machine to be deleted")
			Eventually(func() bool {
				_, err := dynamicClient.Resource(machineGVR).Namespace(testNamespace).Get(ctx, machineName, metav1.GetOptions{})
				return err != nil
			}, machineDeletionTimeout, pollInterval).Should(BeTrue(), "Machine was not deleted")

			By("Cleaning up credentials secret")
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())

			By("Cleaning up infrastructure via NICo API")
			cleanupInfrastructureViaAPI(token, testOrgName, subnetID, vpcID, siteID)
		})
	})
})
