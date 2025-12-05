package model

type BuildStatus string

const (
	StatusNotExist  BuildStatus = "notExist"
	StatusPending   BuildStatus = "pending"
	StatusBuilding  BuildStatus = "building"
	StatusCompleted BuildStatus = "completed"
	StatusFailed    BuildStatus = "failed"
)

type BuildResponse struct {
	Status      BuildStatus `json:"status"`
	JobID       string      `json:"job_id,omitempty"`
	Error       string      `json:"error,omitempty"`
	DownloadURL string      `json:"download_url,omitempty"`
}

type StatusResponse struct {
	Status        BuildStatus `json:"status"`
	JobID         string      `json:"job_id,omitempty"`
	KernelVersion string      `json:"kernel_version,omitempty"`
	CreatedAt     string      `json:"created_at,omitempty"`
	Error         string      `json:"error,omitempty"`
	CachePath     string      `json:"cache_path,omitempty"`
	CompletedAt   string      `json:"completed_at,omitempty"`
	Duration      string      `json:"duration,omitempty"`
	DownloadURL   string      `json:"download_url,omitempty"`
}
