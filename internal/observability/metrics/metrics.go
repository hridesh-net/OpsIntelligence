package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

var histogramBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type latencyHistogram struct {
	count   uint64
	sum     uint64 // stored as atomic fixed-point (seconds * 1e6)
	buckets []atomic.Uint64
}

func newLatencyHistogram() *latencyHistogram {
	h := &latencyHistogram{buckets: make([]atomic.Uint64, len(histogramBuckets))}
	return h
}

func (h *latencyHistogram) observe(seconds float64) {
	atomic.AddUint64(&h.count, 1)
	atomic.AddUint64(&h.sum, uint64(seconds*1e6))
	for i, b := range histogramBuckets {
		if seconds <= b {
			h.buckets[i].Add(1)
			break
		}
	}
}

// Store is a lightweight Prometheus-compatible in-process metric store.
// Labels are intentionally constrained to low-cardinality dimensions only.
type Store struct {
	messagesSentTotal      sync.Map // map[string]*atomic.Uint64
	messagesFailedTotal    sync.Map // map[string]*atomic.Uint64
	messagesReceivedTotal  sync.Map // map[string]*atomic.Uint64
	adapterRetriesTotal    sync.Map // map[string]*atomic.Uint64
	channelReconnectsTotal sync.Map // map[string]*atomic.Uint64
	dlqDepth               sync.Map // map[string]*atomic.Uint64
	messageLatency         sync.Map // map[string]*latencyHistogram

	gatewayUp              atomic.Uint64
	preflightFailuresTotal atomic.Uint64
}

func NewStore() *Store {
	return &Store{}
}

func counterInc(m *sync.Map, key string) {
	v, _ := m.LoadOrStore(key, new(atomic.Uint64))
	v.(*atomic.Uint64).Add(1)
}

func counterGet(m *sync.Map, key string) uint64 {
	v, ok := m.Load(key)
	if !ok {
		return 0
	}
	return v.(*atomic.Uint64).Load()
}

func (s *Store) IncMessagesSent(channel string) {
	counterInc(&s.messagesSentTotal, channel)
}

func (s *Store) IncMessagesFailed(channel string) {
	counterInc(&s.messagesFailedTotal, channel)
}

func (s *Store) IncMessagesReceived(channel string) {
	counterInc(&s.messagesReceivedTotal, channel)
}

func (s *Store) IncAdapterRetries(channel string) {
	counterInc(&s.adapterRetriesTotal, channel)
}

func (s *Store) IncChannelReconnects(channel string) {
	counterInc(&s.channelReconnectsTotal, channel)
}

func (s *Store) ObserveMessageLatency(channel string, seconds float64) {
	v, _ := s.messageLatency.LoadOrStore(channel, newLatencyHistogram())
	v.(*latencyHistogram).observe(seconds)
}

func (s *Store) SetDLQDepth(channel string, depth float64) {
	v, _ := s.dlqDepth.LoadOrStore(channel, new(atomic.Uint64))
	v.(*atomic.Uint64).Store(uint64(depth * 1e6))
}

func (s *Store) SetGatewayUp(up bool) {
	if up {
		s.gatewayUp.Store(1)
		return
	}
	s.gatewayUp.Store(0)
}

// IncPreflightFailures increments when CLI preflight (doctor subset) fails before start.
func (s *Store) IncPreflightFailures() {
	s.preflightFailuresTotal.Add(1)
}

func (s *Store) RenderPrometheus() string {
	var b strings.Builder

	b.WriteString("# HELP messages_sent_total Total outbound messages sent successfully.\n")
	b.WriteString("# TYPE messages_sent_total counter\n")
	writeCounterByChannel(&b, "messages_sent_total", &s.messagesSentTotal)

	b.WriteString("# HELP messages_failed_total Total outbound messages that failed.\n")
	b.WriteString("# TYPE messages_failed_total counter\n")
	writeCounterByChannel(&b, "messages_failed_total", &s.messagesFailedTotal)

	b.WriteString("# HELP messages_received_total Total inbound messages received.\n")
	b.WriteString("# TYPE messages_received_total counter\n")
	writeCounterByChannel(&b, "messages_received_total", &s.messagesReceivedTotal)

	b.WriteString("# HELP adapter_retries_total Total adapter retry attempts.\n")
	b.WriteString("# TYPE adapter_retries_total counter\n")
	writeCounterByChannel(&b, "adapter_retries_total", &s.adapterRetriesTotal)

	b.WriteString("# HELP channel_reconnects_total Total reconnect events by channel.\n")
	b.WriteString("# TYPE channel_reconnects_total counter\n")
	writeCounterByChannel(&b, "channel_reconnects_total", &s.channelReconnectsTotal)

	b.WriteString("# HELP dlq_depth Dead-letter queue depth by channel.\n")
	b.WriteString("# TYPE dlq_depth gauge\n")
	writeGaugeByChannel(&b, "dlq_depth", &s.dlqDepth)

	b.WriteString("# HELP gateway_health Gateway health indicator (1=up,0=down).\n")
	b.WriteString("# TYPE gateway_health gauge\n")
	fmt.Fprintf(&b, "gateway_health %d\n", s.gatewayUp.Load())

	b.WriteString("# HELP preflight_failures_total Total preflight failures before daemon/gateway start.\n")
	b.WriteString("# TYPE preflight_failures_total counter\n")
	fmt.Fprintf(&b, "preflight_failures_total %d\n", s.preflightFailuresTotal.Load())

	b.WriteString("# HELP message_latency_seconds Outbound message latency in seconds.\n")
	b.WriteString("# TYPE message_latency_seconds histogram\n")
	writeLatencyByChannel(&b, &s.messageLatency)

	return b.String()
}

func writeCounterByChannel(b *strings.Builder, name string, m *sync.Map) {
	var keys []string
	m.Range(func(k, v interface{}) bool {
		keys = append(keys, k.(string))
		return true
	})
	sort.Strings(keys)
	for _, ch := range keys {
		fmt.Fprintf(b, "%s{channel=%q} %d\n", name, ch, counterGet(m, ch))
	}
}

func writeGaugeByChannel(b *strings.Builder, name string, m *sync.Map) {
	var keys []string
	m.Range(func(k, v interface{}) bool {
		keys = append(keys, k.(string))
		return true
	})
	sort.Strings(keys)
	for _, ch := range keys {
		v, _ := m.Load(ch)
		val := v.(*atomic.Uint64).Load()
		fmt.Fprintf(b, "%s{channel=%q} %g\n", name, ch, float64(val)/1e6)
	}
}

func writeLatencyByChannel(b *strings.Builder, m *sync.Map) {
	var keys []string
	m.Range(func(k, v interface{}) bool {
		keys = append(keys, k.(string))
		return true
	})
	sort.Strings(keys)
	for _, channel := range keys {
		v, _ := m.Load(channel)
		h := v.(*latencyHistogram)
		count := atomic.LoadUint64(&h.count)
		sum := atomic.LoadUint64(&h.sum)
		for i, upper := range histogramBuckets {
			bucketCount := h.buckets[i].Load()
			fmt.Fprintf(b, "message_latency_seconds_bucket{channel=%q,le=%q} %d\n", channel, trimFloat(upper), bucketCount)
		}
		fmt.Fprintf(b, "message_latency_seconds_bucket{channel=%q,le=\"+Inf\"} %d\n", channel, count)
		fmt.Fprintf(b, "message_latency_seconds_sum{channel=%q} %g\n", channel, float64(sum)/1e6)
		fmt.Fprintf(b, "message_latency_seconds_count{channel=%q} %d\n", channel, count)
	}
}

func trimFloat(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", v), "0"), ".")
}

func (s *Store) ResetForTests() {
	s.messagesSentTotal = sync.Map{}
	s.messagesFailedTotal = sync.Map{}
	s.messagesReceivedTotal = sync.Map{}
	s.adapterRetriesTotal = sync.Map{}
	s.channelReconnectsTotal = sync.Map{}
	s.dlqDepth = sync.Map{}
	s.messageLatency = sync.Map{}
	s.gatewayUp.Store(0)
	s.preflightFailuresTotal.Store(0)
}

var (
	defaultOnce sync.Once
	defaultInst *Store
)

func Default() *Store {
	defaultOnce.Do(func() {
		defaultInst = NewStore()
	})
	return defaultInst
}
