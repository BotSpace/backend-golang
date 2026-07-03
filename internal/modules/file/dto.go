package file

import "time"

// FileResponse is the public projection of a file's metadata.
type FileResponse struct {
	ID        string     `json:"id"`
	UUID      string     `json:"uuid"`
	ProjectID string     `json:"project_id"`
	Name      string     `json:"name"`
	URL       string     `json:"url"`
	Size      int64      `json:"size"`
	FileType  string     `json:"filetype"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func toFileResponse(f *File) FileResponse {
	return FileResponse{
		ID:        f.ID.String(),
		UUID:      f.UUID.String(),
		ProjectID: f.ProjectID.String(),
		Name:      f.OriginalName,
		URL:       "/api/v1/file/" + f.UUID.String() + "/download",
		Size:      f.FileSize,
		FileType:  f.FileType,
		ExpiresAt: f.ExpiresAt,
		CreatedAt: f.CreatedAt,
		UpdatedAt: f.UpdatedAt,
	}
}

// FileListResponse is a paginated envelope for a file listing.
type FileListResponse struct {
	Items  []FileResponse `json:"items"`
	Total  int64          `json:"total"`
	Limit  int32          `json:"limit"`
	Offset int32          `json:"offset"`
}

func toFileListResponse(items []File, total int64, limit, offset int32) FileListResponse {
	out := make([]FileResponse, len(items))
	for i := range items {
		out[i] = toFileResponse(&items[i])
	}
	return FileListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// UploadFileRequest is the multipart form payload for POST /file (the blob
// itself arrives as the "file" form field, read separately via c.FormFile).
type UploadFileRequest struct {
	ProjectID string `json:"project_id" validate:"required,uuid"`
	FileType  string `json:"filetype" validate:"required,oneof=file photo video audio"`
	TTL       int    `json:"ttl" validate:"gte=0"`
}

// UpdateFileRequest carries the one metadata field a client may change after
// upload.
type UpdateFileRequest struct {
	FileType *string `json:"filetype" validate:"omitempty,oneof=file photo video audio"`
}

// ResourceResponse is the public projection of one resource report.
type ResourceResponse struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Hostname  string    `json:"hostname"`
	RAM       int32     `json:"ram"`
	CPU       int32     `json:"cpu"`
	NetRX     int64     `json:"net_rx"`
	NetTX     int64     `json:"net_tx"`
	CreatedAt time.Time `json:"created_at"`
}

func toResourceResponse(r *Resource) ResourceResponse {
	return ResourceResponse{
		ID:        r.ID.String(),
		ProjectID: r.ProjectID.String(),
		Hostname:  r.Hostname,
		RAM:       r.RAM,
		CPU:       r.CPU,
		NetRX:     r.NetRX,
		NetTX:     r.NetTX,
		CreatedAt: r.CreatedAt,
	}
}

// ResourceListResponse is a paginated envelope for a resource listing.
type ResourceListResponse struct {
	Items  []ResourceResponse `json:"items"`
	Total  int64              `json:"total"`
	Limit  int32              `json:"limit"`
	Offset int32              `json:"offset"`
}

func toResourceListResponse(items []Resource, total int64, limit, offset int32) ResourceListResponse {
	out := make([]ResourceResponse, len(items))
	for i := range items {
		out[i] = toResourceResponse(&items[i])
	}
	return ResourceListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// CreateResourceRequest is the payload for POST /resource.
type CreateResourceRequest struct {
	ProjectID string `json:"project_id" validate:"required,uuid"`
	Hostname  string `json:"hostname" validate:"required,min=1,max=255"`
	RAM       int32  `json:"ram" validate:"gte=0"`
	CPU       int32  `json:"cpu" validate:"gte=0"`
	NetRX     int64  `json:"net_rx" validate:"gte=0"`
	NetTX     int64  `json:"net_tx" validate:"gte=0"`
}

// ChartSeries is one named series of a usage chart: parallel dates/values.
type ChartSeries struct {
	Dates  []string  `json:"dates"`
	Values []float64 `json:"values"`
}

// UsedChart mirrors services/used.py's chart() payload shape.
type UsedChart struct {
	RAM     ChartSeries `json:"ram"`
	CPU     ChartSeries `json:"cpu"`
	NetTX   ChartSeries `json:"net_tx"`
	NetRX   ChartSeries `json:"net_rx"`
	Storage ChartSeries `json:"storage"`
}

// UsedResponse mirrors UsedStatSerializer's payload.
type UsedResponse struct {
	UsedStorage int64     `json:"used_storage"`
	UsedRAM     int64     `json:"used_ram"`
	UsedCPU     int64     `json:"used_cpu"`
	UsedNetTX   int64     `json:"used_net_tx"`
	UsedNetRX   int64     `json:"used_net_rx"`
	Chart       UsedChart `json:"chart"`
}

// hourLayout matches Django's chart() strftime("%Y-%m-%d %H:%M").
const hourLayout = "2006-01-02 15:04"

func toUsedResponse(s *UsageStats) UsedResponse {
	ramDates := make([]string, len(s.Chart))
	ram := make([]float64, len(s.Chart))
	cpuDates := make([]string, len(s.Chart))
	cpu := make([]float64, len(s.Chart))
	netTXDates := make([]string, len(s.Chart))
	netTX := make([]float64, len(s.Chart))
	netRXDates := make([]string, len(s.Chart))
	netRX := make([]float64, len(s.Chart))
	for i, p := range s.Chart {
		d := p.Hour.UTC().Format(hourLayout)
		ramDates[i], ram[i] = d, p.RAM
		cpuDates[i], cpu[i] = d, p.CPU
		netTXDates[i], netTX[i] = d, float64(p.NetTX)
		netRXDates[i], netRX[i] = d, float64(p.NetRX)
	}

	storageDates := make([]string, len(s.StorageChart))
	storage := make([]float64, len(s.StorageChart))
	for i, p := range s.StorageChart {
		storageDates[i] = p.Hour.UTC().Format(hourLayout)
		storage[i] = float64(p.Storage)
	}

	return UsedResponse{
		UsedStorage: s.UsedStorage,
		UsedRAM:     s.UsedRAM,
		UsedCPU:     s.UsedCPU,
		UsedNetTX:   s.UsedNetTX,
		UsedNetRX:   s.UsedNetRX,
		Chart: UsedChart{
			RAM:     ChartSeries{Dates: ramDates, Values: ram},
			CPU:     ChartSeries{Dates: cpuDates, Values: cpu},
			NetTX:   ChartSeries{Dates: netTXDates, Values: netTX},
			NetRX:   ChartSeries{Dates: netRXDates, Values: netRX},
			Storage: ChartSeries{Dates: storageDates, Values: storage},
		},
	}
}
