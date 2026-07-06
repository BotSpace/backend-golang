package project

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	prometheusDefaultTimeout = 2 * time.Second
	projectPodPattern        = `^(?:release|debug)-([0-9a-f-]{36})(?:-[0-9]+)?.*`
	prometheusCPUQuery       = `sum by (project_id) (label_replace(rate(container_cpu_usage_seconds_total{namespace="botmother",container="engine",pod=~"(release|debug)-.+"}[5m]), "project_id", "$1", "pod", "` + projectPodPattern + `"))`
	prometheusMemoryQuery    = `sum by (project_id) (label_replace(container_memory_working_set_bytes{namespace="botmother",container="engine",pod=~"(release|debug)-.+"}, "project_id", "$1", "pod", "` + projectPodPattern + `"))`
)

type PrometheusMetricsProvider struct {
	baseURL string
	client  *http.Client
}

func NewPrometheusMetricsProvider(baseURL string) MetricsProvider {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &PrometheusMetricsProvider{
		baseURL: baseURL,
		client:  &http.Client{Timeout: prometheusDefaultTimeout},
	}
}

func (p *PrometheusMetricsProvider) ProjectMetrics(ctx context.Context) (map[uuid.UUID]LiveMetrics, bool, error) {
	cpu, err := p.query(ctx, prometheusCPUQuery)
	if err != nil {
		return nil, false, err
	}
	mem, err := p.query(ctx, prometheusMemoryQuery)
	if err != nil {
		return nil, false, err
	}
	out := make(map[uuid.UUID]LiveMetrics, len(cpu)+len(mem))
	for id, cores := range cpu {
		m := out[id]
		m.CPUMilli = clampInt32(math.Round(cores * 1000))
		out[id] = m
	}
	for id, bytes := range mem {
		m := out[id]
		m.MemoryBytes = clampInt32(math.Round(bytes))
		out[id] = m
	}
	return out, true, nil
}

func (p *PrometheusMetricsProvider) ProjectHistory(ctx context.Context, projectID uuid.UUID, window string) (*MonitoringHistory, bool, error) {
	rng := monitoringRange(window)
	podSelector := fmt.Sprintf(`pod=~"(release|debug)-%s.*"`, projectID.String())
	cpuQuery := fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{namespace="botmother",container="engine",%s}[5m]))`, podSelector)
	memoryQuery := fmt.Sprintf(`sum(container_memory_working_set_bytes{namespace="botmother",container="engine",%s})`, podSelector)
	netRXQuery := fmt.Sprintf(`sum(rate(container_network_receive_bytes_total{namespace="botmother",%s}[5m]))`, podSelector)
	netTXQuery := fmt.Sprintf(`sum(rate(container_network_transmit_bytes_total{namespace="botmother",%s}[5m]))`, podSelector)
	diskQuery := fmt.Sprintf(`sum(container_fs_usage_bytes{namespace="botmother",container="engine",%s})`, podSelector)

	cpu, err := p.queryRange(ctx, cpuQuery, rng)
	if err != nil {
		return nil, false, err
	}
	mem, err := p.queryRange(ctx, memoryQuery, rng)
	if err != nil {
		return nil, false, err
	}
	rx, err := p.queryRange(ctx, netRXQuery, rng)
	if err != nil {
		return nil, false, err
	}
	tx, err := p.queryRange(ctx, netTXQuery, rng)
	if err != nil {
		return nil, false, err
	}
	disk, err := p.queryRange(ctx, diskQuery, rng)
	if err != nil {
		return nil, false, err
	}
	return &MonitoringHistory{
		CPU:       cpu,
		Memory:    mem,
		NetworkRX: rx,
		NetworkTX: tx,
		Disk:      disk,
	}, true, nil
}

func (p *PrometheusMetricsProvider) query(ctx context.Context, promQL string) (map[uuid.UUID]float64, error) {
	endpoint, err := url.Parse(p.baseURL + "/api/v1/query")
	if err != nil {
		return nil, fmt.Errorf("parse prometheus url: %w", err)
	}
	q := endpoint.Query()
	q.Set("query", promQL)
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build prometheus request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query prometheus: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query prometheus: status %d", resp.StatusCode)
	}

	var decoded prometheusQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode prometheus response: %w", err)
	}
	if decoded.Status != "success" {
		return nil, fmt.Errorf("query prometheus: %s", decoded.Error)
	}

	out := make(map[uuid.UUID]float64, len(decoded.Data.Result))
	for _, r := range decoded.Data.Result {
		id, err := uuid.Parse(r.Metric.ProjectID)
		if err != nil || len(r.Value) < 2 {
			continue
		}
		raw, ok := r.Value[1].(string)
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		out[id] = value
	}
	return out, nil
}

func (p *PrometheusMetricsProvider) queryRange(ctx context.Context, promQL string, rng prometheusRange) ([]MetricPoint, error) {
	endpoint, err := url.Parse(p.baseURL + "/api/v1/query_range")
	if err != nil {
		return nil, fmt.Errorf("parse prometheus url: %w", err)
	}
	q := endpoint.Query()
	q.Set("query", promQL)
	q.Set("start", strconv.FormatInt(rng.Start.Unix(), 10))
	q.Set("end", strconv.FormatInt(rng.End.Unix(), 10))
	q.Set("step", strconv.Itoa(int(rng.Step.Seconds())))
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build prometheus request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query prometheus range: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query prometheus range: status %d", resp.StatusCode)
	}

	var decoded prometheusRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode prometheus range response: %w", err)
	}
	if decoded.Status != "success" {
		return nil, fmt.Errorf("query prometheus range: %s", decoded.Error)
	}
	if len(decoded.Data.Result) == 0 {
		return nil, nil
	}
	return parseMetricPoints(decoded.Data.Result[0].Values), nil
}

type prometheusQueryResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Data   struct {
		Result []struct {
			Metric struct {
				ProjectID string `json:"project_id"`
			} `json:"metric"`
			Value []any `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

type prometheusRangeResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Data   struct {
		Result []struct {
			Values [][]any `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

type prometheusRange struct {
	Start time.Time
	End   time.Time
	Step  time.Duration
}

func monitoringRange(window string) prometheusRange {
	end := time.Now()
	duration := 24 * time.Hour
	step := 5 * time.Minute
	switch window {
	case "1h":
		duration = time.Hour
		step = time.Minute
	case "6h":
		duration = 6 * time.Hour
		step = 2 * time.Minute
	case "7d":
		duration = 7 * 24 * time.Hour
		step = time.Hour
	}
	return prometheusRange{Start: end.Add(-duration), End: end, Step: step}
}

func parseMetricPoints(values [][]any) []MetricPoint {
	out := make([]MetricPoint, 0, len(values))
	for _, v := range values {
		if len(v) < 2 {
			continue
		}
		ts, ok := numberAsFloat(v[0])
		if !ok {
			continue
		}
		raw, ok := v[1].(string)
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		out = append(out, MetricPoint{Timestamp: int64(ts), Value: value})
	}
	return out
}

func numberAsFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

func clampInt32(v float64) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}
