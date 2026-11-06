package message

// Only human-readable Result fields enter the product projection. Native usage
// and accounting are handled independently by telemetry.
type ResultContent struct {
	TerminalReason string   `json:"terminal_reason"`
	Subtype        string   `json:"subtype"`
	Result         string   `json:"result"`
	DurationMs     int64    `json:"duration_ms"`
	IsError        bool     `json:"is_error"`
	Errors         []string `json:"errors"`
}
