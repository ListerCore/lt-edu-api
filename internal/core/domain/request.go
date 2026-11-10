package domain

// RequestMeta identifies who is calling (logs, rate limits, audit trail)
type RequestMeta struct {
	IP        string
	UserAgent string
}
