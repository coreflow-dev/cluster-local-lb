package controller

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

var _ = Describe("HealthProber Unit Tests", func() {
	var (
		events chan event.GenericEvent
		prober *HealthProber
		ctx    context.Context
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		events = make(chan event.GenericEvent, 100)
		prober = NewHealthProber(events)
		ctx, cancel = context.WithCancel(context.Background())
	})

	AfterEach(func() {
		cancel()
	})

	Context("State Transitions and Thresholds", func() {
		It("should fail: initial state reports healthy before any probe runs (False Positive Bug)", func() {
			key := TargetKey{
				NamespacedName: types.NamespacedName{Name: "test-lb", Namespace: "default"},
				IP:             "127.0.0.1",
			}
			spec := TargetSpec{Port: 8080, UnhealthyThreshold: 2}

			prober.RegisterTarget(key, spec)

			Expect(prober.IsHealthy(key)).To(BeFalse(), "New target should be unready until proven healthy")
		})

		It("correctly transitions state only after reaching UnhealthyThreshold", func() {
			var statusCode atomic.Int32
			statusCode.Store(http.StatusOK)

			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(int(statusCode.Load()))
			}))
			defer server.Close()

			u, _ := url.Parse(server.URL)
			host, portStr, _ := net.SplitHostPort(u.Host)
			port, _ := strconv.Atoi(portStr)

			key := TargetKey{
				NamespacedName: types.NamespacedName{Name: "lb", Namespace: "default"},
				IP:             host,
			}
			spec := TargetSpec{
				Port:               int32(port),
				Path:               "/",
				IntervalSeconds:    1,
				TimeoutSeconds:     3,
				UnhealthyThreshold: 2,
				HealthyThreshold:   1,
			}

			prober.client = server.Client()

			prober.RegisterTarget(key, spec)
			Expect(prober.IsHealthy(key)).To(BeFalse())

			// transition to healthy
			prober.probeSingle(ctx, key, spec)
			Expect(prober.IsHealthy(key)).To(BeTrue(), "Should be healthy after 1 success")

			statusCode.Store(http.StatusInternalServerError)

			// Probe 1: Failure 1 (Below threshold of 2)
			prober.probeSingle(ctx, key, spec)
			Expect(prober.IsHealthy(key)).To(BeTrue(), "Should remain healthy after 1 failure")

			// Probe 2: Failure 2 (Reaches threshold)
			prober.probeSingle(ctx, key, spec)
			Expect(prober.IsHealthy(key)).To(BeFalse(), "Should become unhealthy after 2 failures")

			Eventually(events).Should(Receive())
		})
	})

	Context("Concurrency and Race Conditions", func() {
		It("handles concurrent registrations, probes, and deletions without data races", func() {
			go prober.Start(ctx)

			var wg sync.WaitGroup
			for i := range 50 {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					key := TargetKey{
						NamespacedName: types.NamespacedName{Name: "lb", Namespace: "default"},
						IP:             "10.0.0." + strconv.Itoa(id),
					}
					spec := TargetSpec{Port: 6443, IntervalSeconds: 1}

					prober.RegisterTarget(key, spec)
					_ = prober.IsHealthy(key)
					if id%2 == 0 {
						prober.UnregisterTarget(key)
					}
				}(i)
			}
			wg.Wait()
		})
	})
})
