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
	prometheusCPUQuery       = `sum by (label_project_id) (rate(container_cpu_usage_seconds_total{namespace="botmother",container!="",pod!=""}[5m]) * on(namespace,pod) group_left(label_project_id) kube_pod_labels{namespace="botmother",label_app="botmother-engine",label_project_id!=""})`
	prometheusMemoryQuery    = `sum by (label_project_id) (container_memory_working_set_bytes{namespace="botmother",container!="",pod!=""} * on(namespace,pod) group_left(label_project_id) kube_pod_labels{namespace="botmother",label_app="botmother-engine",label_project_id!=""})`
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

type prometheusQueryResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Data   struct {
		Result []struct {
			Metric struct {
				ProjectID string `json:"label_project_id"`
			} `json:"metric"`
			Value []any `json:"value"`
		} `json:"result"`
	} `json:"data"`
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
