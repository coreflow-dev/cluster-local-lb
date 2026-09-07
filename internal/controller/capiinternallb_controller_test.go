/*
Copyright 2026.

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

package controller

import (
	// "context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	// ctrl "sigs.k8s.io/controller-runtime"
	// "sigs.k8s.io/controller-runtime/pkg/event"
	// "sigs.k8s.io/controller-runtime/pkg/reconcile"

	internallbv1alpha1 "github.com/coreflow-dev/cluster-local-lb.git/api/v1alpha1"
)

// Helper to build dynamic CAPI Machine objects
func createCAPIMachine(name, namespace, clusterName string, isControlPlane bool, internalIP, externalIP string, malformedAddrs bool) *unstructured.Unstructured {
	m := &unstructured.Unstructured{}
	m.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta2",
		Kind:    "Machine",
	})
	m.SetName(name)
	m.SetNamespace(namespace)

	labels := map[string]string{
		"cluster.x-k8s.io/cluster-name": clusterName,
	}
	if isControlPlane {
		labels["cluster.x-k8s.io/control-plane"] = ""
	}
	m.SetLabels(labels)

	spec := map[string]any{
		"clusterName": clusterName,
		"bootstrap":   map[string]any{},
		"infrastructureRef": map[string]any{
			"apiGroup":   "infrastructure.cluster.x-k8s.io",
			"apiVersion": "v1beta1",
			"kind":       "DockerMachine",
			"name":       name + "-infra",
		},
	}
	m.Object["spec"] = spec

	Expect(k8sClient.Create(ctx, m)).To(Succeed())

	if malformedAddrs {
		_ = unstructured.SetNestedField(m.Object, "invalid-address-format", "status", "addresses")
	} else {
		var addrs []any
		if internalIP != "" {
			addrs = append(addrs, map[string]any{"type": "InternalIP", "address": internalIP})
		}
		if externalIP != "" {
			addrs = append(addrs, map[string]any{"type": "ExternalIP", "address": externalIP})
		}
		_ = unstructured.SetNestedSlice(m.Object, addrs, "status", "addresses")
	}

	Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())

	return m
}

/*var _ = Describe("CapiInternalLb Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-resource"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}
		capiinternallb := &internallbv1alpha1.CapiInternalLb{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind CapiInternalLb")
			err := k8sClient.Get(ctx, typeNamespacedName, capiinternallb)
			if err != nil && errors.IsNotFound(err) {
				resource := &internallbv1alpha1.CapiInternalLb{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					// TODO(user): Specify other spec details if needed.
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &internallbv1alpha1.CapiInternalLb{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance CapiInternalLb")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")

			ev := make(chan event.GenericEvent, 100)
			prober := NewHealthProber(ev)
			controllerReconciler := &CapiInternalLbReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Prober: prober,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})*/

var _ = Describe("CapiInternalLb Controller", func() {
	const (
		timeout  = 5 * time.Second
		interval = 100 * time.Millisecond
	)

	var (
		testNamespace *corev1.Namespace
		nsName        string
	)

	BeforeEach(func() {
		nsName = fmt.Sprintf("test-ns-%d", time.Now().UnixNano())
		testNamespace = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
		Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
	})

	AfterEach(func() {
		_ = k8sClient.Delete(ctx, testNamespace)
	})

	// -------------------------------------------------------------------
	// 1. MANAGER-DRIVEN INTEGRATION TESTS (Async with Eventually)
	// -------------------------------------------------------------------
	Context("Manager Integration Reconciliation", func() {
		It("should inject finalizer, create Service, and build EndpointSlice automatically", func() {
			crName := "integration-lb"
			clusterName := "cluster-alpha"

			_ = createCAPIMachine("cp-1", nsName, clusterName, true, "10.0.0.5", "", false)

			cr := &internallbv1alpha1.CapiInternalLb{
				ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: nsName},
				Spec: internallbv1alpha1.CapiInternalLbSpec{
					ClusterRef: corev1.ObjectReference{Name: clusterName},
					TargetPort: 6443,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Verify finalizer added by Manager
			Eventually(func(g Gomega) {
				fetched := &internallbv1alpha1.CapiInternalLb{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: crName, Namespace: nsName}, fetched)).To(Succeed())
				g.Expect(fetched.Finalizers).To(ContainElement(lbFinalizer))
			}, timeout, interval).Should(Succeed())

			// Verify Service created
			svcName := crName + "-lb"
			Eventually(func(g Gomega) {
				svc := &corev1.Service{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: svcName, Namespace: nsName}, svc)).To(Succeed())
				g.Expect(svc.Spec.Ports[0].Port).To(Equal(int32(6443)))
			}, timeout, interval).Should(Succeed())

			// Verify EndpointSlice created with unready endpoint
			sliceName := svcName + "-slice"
			Eventually(func(g Gomega) {
				slice := &discoveryv1.EndpointSlice{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: sliceName, Namespace: nsName}, slice)).To(Succeed())
				g.Expect(slice.Endpoints).To(HaveLen(1))
				g.Expect(slice.Endpoints[0].Addresses).To(ContainElement("10.0.0.5"))
				g.Expect(*slice.Endpoints[0].Conditions.Ready).To(BeFalse())
			}, timeout, interval).Should(Succeed())
		})

		It("should update EndpointSlice automatically when a new CAPI Machine is created (Watches)", func() {
			crName := "watch-lb"
			clusterName := "cluster-watch"

			cr := &internallbv1alpha1.CapiInternalLb{
				ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: nsName},
				Spec: internallbv1alpha1.CapiInternalLbSpec{
					ClusterRef: corev1.ObjectReference{Name: clusterName},
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			sliceName := crName + "-lb-slice"
			Eventually(func(g Gomega) {
				slice := &discoveryv1.EndpointSlice{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: sliceName, Namespace: nsName}, slice)).To(Succeed())
				g.Expect(slice.Endpoints).To(BeEmpty())
			}, timeout, interval).Should(Succeed())

			_ = createCAPIMachine("cp-dynamic", nsName, clusterName, true, "10.0.0.99", "", false)

			Eventually(func(g Gomega) {
				slice := &discoveryv1.EndpointSlice{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: sliceName, Namespace: nsName}, slice)).To(Succeed())
				g.Expect(slice.Endpoints).To(HaveLen(1))
				g.Expect(slice.Endpoints[0].Addresses).To(ContainElement("10.0.0.99"))
			}, timeout, interval).Should(Succeed())
		})

		It("should remove finalizer and allow resource deletion", func() {
			crName := "delete-lb"
			cr := &internallbv1alpha1.CapiInternalLb{
				ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: nsName},
				Spec: internallbv1alpha1.CapiInternalLbSpec{
					ClusterRef: corev1.ObjectReference{Name: "cluster-del"},
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Wait for finalizer to be added
			Eventually(func(g Gomega) {
				fetched := &internallbv1alpha1.CapiInternalLb{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: crName, Namespace: nsName}, fetched)).To(Succeed())
				g.Expect(fetched.Finalizers).To(ContainElement(lbFinalizer))
			}, timeout, interval).Should(Succeed())

			fetched := &internallbv1alpha1.CapiInternalLb{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: crName, Namespace: nsName}, fetched)).To(Succeed())
			Expect(k8sClient.Delete(ctx, fetched)).To(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: crName, Namespace: nsName}, fetched)
				return errors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		})
	})

	// -------------------------------------------------------------------
	// 2. DIRECT UNIT TESTS (Synchronous Helper Logic)
	// -------------------------------------------------------------------
	Context("Unit Tests for Isolated Helper Methods", func() {
		It("should correctly select IPs based on IPTypeSelection rule", func() {
			addresses := []any{
				map[string]any{"type": "InternalIP", "address": "10.0.0.1"},
				map[string]any{"type": "ExternalIP", "address": "1.2.3.4"},
			}

			// Internal
			ip, err := selectMachineIP(addresses, internallbv1alpha1.IPTypeInternal)
			Expect(err).NotTo(HaveOccurred())
			Expect(ip).To(Equal("10.0.0.1"))

			// External
			ip, err = selectMachineIP(addresses, internallbv1alpha1.IPTypeExternal)
			Expect(err).NotTo(HaveOccurred())
			Expect(ip).To(Equal("1.2.3.4"))

			// Auto (prefers InternalIP)
			ip, err = selectMachineIP(addresses, internallbv1alpha1.IPTypeAuto)
			Expect(err).NotTo(HaveOccurred())
			Expect(ip).To(Equal("10.0.0.1"))

			// Fallback error when requesting External from Internal-only machine
			internalOnly := []any{
				map[string]any{"type": "InternalIP", "address": "10.0.0.1"},
			}
			_, err = selectMachineIP(internalOnly, internallbv1alpha1.IPTypeExternal)
			Expect(err).To(HaveOccurred())
		})
	})
})
