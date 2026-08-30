package dto

import "time"

// AlertWebhook 是 Alertmanager webhook 请求体。
type AlertWebhook struct {
	Status string  `json:"status"`
	Alerts []Alert `json:"alerts"`
}

// Alert 是单条告警。
type Alert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      time.Time         `json:"endsAt"`
	Fingerprint string            `json:"fingerprint"`
}

// AlertWebhookResponse 是 webhook 响应。Errors 记录逐条处理失败的告警明细，
// 便于 Alertmanager 侧排查（不再静默吞错）。
type AlertWebhookResponse struct {
	Created  []string `json:"created"`
	Resolved []string `json:"resolved"`
	Errors   []string `json:"errors,omitempty"`
}
