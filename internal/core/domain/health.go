package domain

import "time"

type Status string

const (
	StatusOK       Status = "ok"
	StatusDegraded Status = "degraded"
)

type ComponentStatus struct {
	Status  Status `json:"status"`
	Latency string `json:"latency,omitempty"`
	Error   string `json:"error,omitempty"`
}

type Health struct {
	Status    Status           `json:"status"`
	Service   string           `json:"service"`
	Timestamp time.Time        `json:"timestamp"`
	DB        *ComponentStatus `json:"db,omitempty"`
	Redis     *ComponentStatus `json:"redis,omitempty"`
}
