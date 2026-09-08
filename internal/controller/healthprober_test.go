package controller

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

type dummyTransport struct{}

func (d *dummyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
	}, nil
}

type selectiveTransport struct{}

func (s *selectiveTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host, _, err := net.SplitHostPort(req.URL.Host)
	if err != nil {
		host = req.URL.Host
	}

	// Extract target ID from IP (10.0.0.X)
	parts := strings.Split(host, ".")
	id, _ := strconv.Atoi(parts[len(parts)-1])

	// Fail every 4th machine (e.g., IDs 3, 7, 11, 15...)
	if id%4 == 3 {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       http.NoBody,
		}, nil
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
	}, nil
}

const ns = "default"

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
				NamespacedName: types.NamespacedName{Name: "test-lb", Namespace: ns},
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
				NamespacedName: types.NamespacedName{Name: "lb", Namespace: ns},
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
			testCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			prober.client = &http.Client{Transport: &dummyTransport{}}

			var wg sync.WaitGroup
			for i := range 50 {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					key := TargetKey{
						NamespacedName: types.NamespacedName{Name: "lb", Namespace: ns},
						IP:             "10.0.0." + strconv.Itoa(id),
					}
					spec := TargetSpec{
						Port:             6443,
						IntervalSeconds:  1,
						HealthyThreshold: 1,
					}

					prober.RegisterTarget(key, spec)
					_ = prober.IsHealthy(key)

					if id%2 == 0 {
						prober.UnregisterTarget(key)
					}
				}(i)
			}
			wg.Wait()

			go func() {
				defer GinkgoRecover()
				_ = prober.Start(testCtx)
			}()

			Eventually(func() bool {
				for i := range 50 {
					key := TargetKey{
						NamespacedName: types.NamespacedName{Name: "lb", Namespace: ns},
						IP:             "10.0.0." + strconv.Itoa(i),
					}
					isHealthy := prober.IsHealthy(key)

					if i%2 != 0 && !isHealthy {
						return false
					}
					if i%2 == 0 && isHealthy {
						return false
					}
				}
				return true
			}, "3s", "100ms").Should(BeTrue(), "Registered targets should become healthy, unregistered targets stay false")
		})
		It("correctly identifies failing targets alongside unregistered ones under concurrency", func() {
			testCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			prober.client = &http.Client{Transport: &selectiveTransport{}}

			var wg sync.WaitGroup
			for i := range 50 {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					key := TargetKey{
						NamespacedName: types.NamespacedName{Name: "lb", Namespace: ns},
						IP:             "10.0.0." + strconv.Itoa(id),
					}
					spec := TargetSpec{
						Port:             6443,
						IntervalSeconds:  1,
						HealthyThreshold: 1,
					}

					prober.RegisterTarget(key, spec)
					if id%2 == 0 {
						prober.UnregisterTarget(key)
					}
				}(i)
			}
			wg.Wait()

			go func() {
				defer GinkgoRecover()
				_ = prober.Start(testCtx)
			}()

			Eventually(func() bool {
				for i := range 50 {
					key := TargetKey{
						NamespacedName: types.NamespacedName{Name: "lb", Namespace: ns},
						IP:             "10.0.0." + strconv.Itoa(i),
					}
					isHealthy := prober.IsHealthy(key)

					if i%2 == 0 {
						if isHealthy {
							return false
						}
					} else if i%4 == 3 {
						// Odd IDs matching % 4 == 3 (e.g. 3, 7, 11): Registered but failing HTTP -> must stay false
						if isHealthy {
							return false
						}
					} else {
						if !isHealthy {
							return false
						}
					}
				}
				return true
			}, "3s", "100ms").Should(BeTrue(), "Only registered, healthy targets should return true")
		})
	})
})
