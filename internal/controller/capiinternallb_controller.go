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
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	internallbv1alpha1 "github.com/coreflow-dev/cluster-local-lb.git/api/v1alpha1"
)

const lbFinalizer = "internallb.schaber.io/capi-internal-lb"

func machineGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   "cluster.x-k8s.io",
		Version: "v1beta2",
		Kind:    "Machine",
	}
}

// TargetKey uniquely identifies an IP probed on behalf of a CapiInternalLb CR.
type TargetKey struct {
	NamespacedName types.NamespacedName
	IP             string
}

type TargetSpec struct {
	Port               int32
	Path               string
	TimeoutSeconds     int32
	IntervalSeconds    int32
	HealthyThreshold   int32
	UnhealthyThreshold int32
}

type TargetState struct {
	Spec                 TargetSpec
	Status               bool
	ConsecutiveSuccesses int32
	ConsecutiveFailures  int32
	LastProbed           time.Time
}

// HealthProber manages background HTTP probes out-of-band from the Reconcile loop.
type HealthProber struct {
	mu     sync.RWMutex
	states map[TargetKey]*TargetState
	events chan event.GenericEvent
	client *http.Client
}

func NewHealthProber(events chan event.GenericEvent) *HealthProber {
	// Custom transport ignoring self-signed TLS certs common on control plane endpoints
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &HealthProber{
		states: make(map[TargetKey]*TargetState),
		events: events,
		client: &http.Client{Transport: tr},
	}
}

func (p *HealthProber) RegisterTarget(key TargetKey, spec TargetSpec) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if spec.Path == "" {
		spec.Path = "/readyz"
	}
	if spec.TimeoutSeconds <= 0 {
		spec.TimeoutSeconds = 3
	}
	if spec.IntervalSeconds <= 0 {
		spec.IntervalSeconds = 10
	}
	if spec.HealthyThreshold <= 0 {
		spec.HealthyThreshold = 1
	}
	if spec.UnhealthyThreshold <= 0 {
		spec.UnhealthyThreshold = 3
	}

	// ToDo: we could track active probes running, cancel and reschedule here
	// so that they use the new config, right now running probes continue to use the old spec
	// so updates are a bit delayed
	state, exists := p.states[key]
	if !exists {
		p.states[key] = &TargetState{
			Spec:   spec,
			Status: false, // Assume unhealthy initially (until proven otherwise)
		}
	} else {
		state.Spec = spec
	}
}

func (p *HealthProber) UnregisterTarget(key TargetKey) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.states, key)
}

func (p *HealthProber) Start(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			p.evaluateAndProbeDueTargets(ctx)
		}
	}
}

func (p *HealthProber) evaluateAndProbeDueTargets(ctx context.Context) {
	now := time.Now()
	var targetsToProbe []struct {
		key  TargetKey
		spec TargetSpec
	}

	{
		p.mu.Lock()
		defer p.mu.Unlock()
		for key, state := range p.states {
			interval := time.Duration(state.Spec.IntervalSeconds) * time.Second
			if now.Sub(state.LastProbed) >= interval {
				state.LastProbed = now
				targetsToProbe = append(targetsToProbe, struct {
					key  TargetKey
					spec TargetSpec
				}{key: key, spec: state.Spec})
			}
		}
	}

	for _, target := range targetsToProbe {
		go p.probeSingle(ctx, target.key, target.spec)
	}
}

func (p *HealthProber) probeSingle(ctx context.Context, key TargetKey, spec TargetSpec) {
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(spec.TimeoutSeconds)*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://%s:%d%s", key.IP, spec.Port, spec.Path)
	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)

	isHealthySample := false
	if err == nil {
		resp, err := p.client.Do(req)
		if err == nil {
			isHealthySample = resp.StatusCode == http.StatusOK
			_ = resp.Body.Close()
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	state, exists := p.states[key]
	if !exists {
		// unregistered, while in flight
		return
	}

	statusChanged := false

	if isHealthySample {
		state.ConsecutiveSuccesses++
		state.ConsecutiveFailures = 0

		if !state.Status && state.ConsecutiveSuccesses >= spec.HealthyThreshold {
			state.Status = true
			statusChanged = true
		}
	} else {
		state.ConsecutiveFailures++
		state.ConsecutiveSuccesses = 0

		if state.Status && state.ConsecutiveFailures >= spec.UnhealthyThreshold {
			state.Status = false
			statusChanged = true
		}
	}

	if statusChanged {
		select {
		case p.events <- event.GenericEvent{
			Object: &internallbv1alpha1.CapiInternalLb{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.NamespacedName.Name,
					Namespace: key.NamespacedName.Namespace,
				},
			},
		}:
		default:
			// reconcile queued anyways
		}
	}
}

func (p *HealthProber) IsHealthy(key TargetKey) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if state, ok := p.states[key]; ok {
		return state.Status
	}
	return false
}

// ToDo: make key 2-level to speed these up?
func (p *HealthProber) CleanupStaleTargets(crName types.NamespacedName, activeKeys map[TargetKey]bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for key := range p.states {
		if key.NamespacedName == crName && !activeKeys[key] {
			delete(p.states, key)
		}
	}
}

// idempotent
func (p *HealthProber) CleanupResource(crName types.NamespacedName) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for key := range p.states {
		if key.NamespacedName == crName {
			delete(p.states, key)
		}
	}
}

// CapiInternalLbReconciler reconciles a CapiInternalLb object
type CapiInternalLbReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Prober       *HealthProber
	ProberEvents chan event.GenericEvent
}

// +kubebuilder:rbac:groups=internallb.io.schaber,resources=capiinternallbs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=internallb.io.schaber,resources=capiinternallbs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=internallb.io.schaber,resources=capiinternallbs/finalizers,verbs=update
// service & capi resources we also need access to:
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;machines,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CapiInternalLb object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/reconcile
func (r *CapiInternalLbReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	if r.Prober == nil {
		return ctrl.Result{}, fmt.Errorf("prober is not initialized")
	}

	cr := &internallbv1alpha1.CapiInternalLb{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		if errors.IsNotFound(err) {
			// in case finalizers do not run (force delete)
			r.Prober.CleanupResource(req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// currently we do not really need a finalizer, since we don't access anything on the resource for deletion
	if !cr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(cr, lbFinalizer) {
			r.Prober.CleanupResource(req.NamespacedName)
			controllerutil.RemoveFinalizer(cr, lbFinalizer)
			if err := r.Update(ctx, cr); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}
	if !controllerutil.ContainsFinalizer(cr, lbFinalizer) {
		controllerutil.AddFinalizer(cr, lbFinalizer)
		if err := r.Update(ctx, cr); err != nil {
			return ctrl.Result{}, err
		}
	}

	svcName := cr.Spec.ServiceName
	if svcName == "" {
		svcName = cr.Name + "-lb"
	}
	targetPort := cr.Spec.TargetPort
	if targetPort == 0 {
		targetPort = 6443
	}

	cpIPs, err := r.fetchControlPlaneIPs(ctx, cr)
	if err != nil {
		logger.Error(err, "Failed to discover control plane IPs")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	endpoints := make([]discoveryv1.Endpoint, 0, len(cpIPs))
	activeKeys := make(map[TargetKey]bool)
	for _, ip := range cpIPs {
		key := TargetKey{NamespacedName: req.NamespacedName, IP: ip}
		activeKeys[key] = true

		spec := TargetSpec{
			Port: targetPort,
		}
		if cr.Spec.HealthCheck != nil {
			spec.Path = cr.Spec.HealthCheck.Path
			spec.IntervalSeconds = cr.Spec.HealthCheck.IntervalSeconds
			spec.TimeoutSeconds = cr.Spec.HealthCheck.TimeoutSeconds
			spec.HealthyThreshold = cr.Spec.HealthCheck.HealthyThreshold
			spec.UnhealthyThreshold = cr.Spec.HealthCheck.UnhealthyThreshold
		}

		r.Prober.RegisterTarget(key, spec)

		isReady := r.Prober.IsHealthy(key)
		endpoints = append(endpoints, discoveryv1.Endpoint{
			Addresses: []string{ip},
			Conditions: discoveryv1.EndpointConditions{
				Ready: new(isReady),
			},
		})
	}
	r.Prober.CleanupStaleTargets(req.NamespacedName, activeKeys)

	if err := r.reconcileService(ctx, cr, svcName, targetPort); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileEndpointSlice(ctx, cr, svcName, targetPort, endpoints); err != nil {
		return ctrl.Result{}, err
	}

	// interval := 10 * time.Second
	// return ctrl.Result{RequeueAfter: interval}, nil
	return ctrl.Result{}, nil
}

func (r *CapiInternalLbReconciler) reconcileService(ctx context.Context, cr *internallbv1alpha1.CapiInternalLb, svcName string, port int32) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: cr.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(cr, svc, r.Scheme); err != nil {
			return err
		}
		// Service without selectors tells K8s to rely on manually managed EndpointSlices
		svc.Spec.Selector = nil
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		svc.Spec.Ports = []corev1.ServicePort{
			{
				Name:       "https",
				Port:       port,
				TargetPort: intstr.FromInt32(port),
				Protocol:   corev1.ProtocolTCP,
			},
		}
		return nil
	})
	return err
}

func (r *CapiInternalLbReconciler) reconcileEndpointSlice(
	ctx context.Context,
	cr *internallbv1alpha1.CapiInternalLb,
	svcName string,
	port int32,
	endpoints []discoveryv1.Endpoint,
) error {
	sliceName := fmt.Sprintf("%s-slice", svcName)
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sliceName,
			Namespace: cr.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, slice, func() error {
		if err := controllerutil.SetControllerReference(cr, slice, r.Scheme); err != nil {
			return err
		}
		// Crucial label linking this EndpointSlice to the target Service for kube-proxy
		if slice.Labels == nil {
			slice.Labels = make(map[string]string)
		}
		slice.Labels[discoveryv1.LabelServiceName] = svcName
		slice.Labels[discoveryv1.LabelManagedBy] = "capi-internal-lb-controller"

		slice.AddressType = discoveryv1.AddressTypeIPv4
		slice.Endpoints = endpoints
		slice.Ports = []discoveryv1.EndpointPort{
			{
				Name:     new("https"),
				Port:     new(port),
				Protocol: new(corev1.ProtocolTCP),
			},
		}
		return nil
	})
	return err
}

func selectMachineIP(addresses []any, selection internallbv1alpha1.IPTypeSelection) (string, error) {
	var internalIP, externalIP string

	for _, addr := range addresses {
		addrMap, ok := addr.(map[string]any)
		if !ok {
			continue
		}
		addrType, ok := addrMap["type"].(string)
		if !ok {
			continue
		}
		ip, ok := addrMap["address"].(string)
		if !ok {
			continue
		}
		switch addrType {
		case internallbv1alpha1.IPSelectorInternal:
			if internalIP == "" {
				internalIP = ip
			}
		case internallbv1alpha1.IPSelectorExternal:
			if externalIP == "" {
				externalIP = ip
			}
		}
	}

	switch selection {
	case internallbv1alpha1.IPTypeInternal:
		if internalIP != "" {
			return internalIP, nil
		}
	case internallbv1alpha1.IPTypeExternal:
		if externalIP != "" {
			return externalIP, nil
		}
	case internallbv1alpha1.IPTypeAuto:
		if internalIP != "" {
			return internalIP, nil
		}
		if externalIP != "" {
			return externalIP, nil
		}
	}

	return "", fmt.Errorf("no matching IP found for selection rule: %s", selection)
}

func (r *CapiInternalLbReconciler) fetchControlPlaneIPs(ctx context.Context, cr *internallbv1alpha1.CapiInternalLb) ([]string, error) {
	machines := &unstructured.UnstructuredList{}
	machines.SetGroupVersionKind(machineGVK())
	targetNamespace := cr.Namespace
	if cr.Spec.ClusterRef.Namespace != "" {
		targetNamespace = cr.Spec.ClusterRef.Namespace
	}
	listOpts := []client.ListOption{
		client.InNamespace(targetNamespace),
		client.MatchingLabels{
			"cluster.x-k8s.io/cluster-name":  cr.Spec.ClusterRef.Name,
			"cluster.x-k8s.io/control-plane": "",
		},
	}

	if err := r.List(ctx, machines, listOpts...); err != nil {
		return nil, err
	}

	var ips []string
	for _, item := range machines.Items {
		addresses, found, _ := unstructured.NestedSlice(item.Object, "status", "addresses")
		if !found {
			continue
		}
		ip, err := selectMachineIP(addresses, cr.Spec.IPType)
		if err == nil {
			ips = append(ips, ip)
		}
	}
	return ips, nil
}

func (r *CapiInternalLbReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Used to route machine update to appropriate lb
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&internallbv1alpha1.CapiInternalLb{},
		"spec.clusterRef.name",
		func(rawObj client.Object) []string {
			lb, ok := rawObj.(*internallbv1alpha1.CapiInternalLb)
			if !ok || lb.Spec.ClusterRef.Name == "" {
				return nil
			}
			return []string{lb.Spec.ClusterRef.Name}
		},
	); err != nil {
		return fmt.Errorf("failed to register indexer: %w", err)
	}

	if r.ProberEvents == nil {
		r.ProberEvents = make(chan event.GenericEvent, 100)
	}
	if r.Prober == nil {
		r.Prober = NewHealthProber(r.ProberEvents)
		if err := mgr.Add(r.Prober); err != nil {
			return fmt.Errorf("failed to add health prober to manager: %w", err)
		}
	}

	// Define an Unstructured Machine prototype for watching CAPI Machine events
	uMachine := &unstructured.Unstructured{}
	uMachine.SetGroupVersionKind(machineGVK())

	return ctrl.NewControllerManagedBy(mgr).
		For(&internallbv1alpha1.CapiInternalLb{}).
		Owns(&corev1.Service{}).
		Owns(&discoveryv1.EndpointSlice{}).
		WatchesRawSource(
			source.Channel(r.ProberEvents, &handler.EnqueueRequestForObject{}),
		).
		Watches(
			uMachine,
			handler.EnqueueRequestsFromMapFunc(r.mapMachineToCapiLb),
		).
		Named("capiinternallb").
		Complete(r)
}

// mapMachineToCapiLb triggers reconcile for CapiInternalLb resources referencing the Machine's cluster.
func (r *CapiInternalLbReconciler) mapMachineToCapiLb(ctx context.Context, obj client.Object) []ctrl.Request {
	labels := obj.GetLabels()
	clusterName, ok := labels["cluster.x-k8s.io/cluster-name"]
	if !ok {
		return nil
	}

	if _, isCP := labels["cluster.x-k8s.io/control-plane"]; !isCP {
		return nil
	}

	var lbList internallbv1alpha1.CapiInternalLbList
	if err := r.List(ctx, &lbList, client.MatchingFields{"spec.clusterRef.name": clusterName}); err != nil {
		return nil
	}

	machineNS := obj.GetNamespace()
	var requests []ctrl.Request
	for _, lb := range lbList.Items {
		expectedMachineNS := lb.Namespace
		if lb.Spec.ClusterRef.Namespace != "" {
			expectedMachineNS = lb.Spec.ClusterRef.Namespace
		}
		if machineNS == expectedMachineNS {
			requests = append(requests, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      lb.Name,
					Namespace: lb.Namespace,
				},
			})
		}
	}
	return requests
}
