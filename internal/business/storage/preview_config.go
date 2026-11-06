package storage

import (
	"errors"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	applicationtask "github.com/talgent-work/workbench/internal/business/storage/application/task"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// PreviewConfig is Storage's whole preview configuration, so a change applies
// alike to File Space and workspace files. Every number is a conservative
// starting point, not capacity-tested; zero or empty keeps its default.
type PreviewConfig struct {
	// GotenbergAddress is the converter's base URL, such as http://gotenberg:3000.
	GotenbergAddress string `json:"gotenberg-address" yaml:"gotenberg-address" toml:"gotenberg-address"`
	// ClientTimeout bounds one conversion request (65s): longer than the
	// converter's own 60s, so its timeout is the one that answers.
	ClientTimeout string `json:"client-timeout" yaml:"client-timeout" toml:"client-timeout"`
	// TaskDeadline bounds a rendering from its request, busy retries
	// included (90s).
	TaskDeadline string `json:"task-deadline" yaml:"task-deadline" toml:"task-deadline"`
	// Retention is how long a Preview Copy or a recorded failure holds after
	// it was made (720h).
	Retention string `json:"retention" yaml:"retention" toml:"retention"`
	// OfficeSourceBytes bounds an office file that is rendered.
	OfficeSourceBytes int64 `json:"office-source-bytes" yaml:"office-source-bytes" toml:"office-source-bytes"`
	// RenderedPDFBytes bounds the PDF a rendering produces.
	RenderedPDFBytes int64 `json:"rendered-pdf-bytes" yaml:"rendered-pdf-bytes" toml:"rendered-pdf-bytes"`
	// NativePDFBytes bounds a PDF shown as it is.
	NativePDFBytes int64 `json:"native-pdf-bytes" yaml:"native-pdf-bytes" toml:"native-pdf-bytes"`
	// ImageBytes bounds an image shown as it is.
	ImageBytes int64 `json:"image-bytes" yaml:"image-bytes" toml:"image-bytes"`
	// TextBytes is how much of a text file is shown.
	TextBytes int64 `json:"text-bytes" yaml:"text-bytes" toml:"text-bytes"`
	// MarkupBytes bounds a rendered HTML or SVG file.
	MarkupBytes int64 `json:"markup-bytes" yaml:"markup-bytes" toml:"markup-bytes"`
}

const defaultClientTimeout = 65 * time.Second

func (c PreviewConfig) validate() error {
	if strings.TrimSpace(c.GotenbergAddress) == "" {
		return errors.New("storage: preview gotenberg-address is required")
	}
	if min(c.OfficeSourceBytes, c.RenderedPDFBytes, c.NativePDFBytes, c.ImageBytes, c.TextBytes, c.MarkupBytes) < 0 {
		return errors.New("storage: preview limits must not be negative")
	}
	for _, value := range []string{c.ClientTimeout, c.TaskDeadline, c.Retention} {
		if _, err := parseDuration(value, time.Second); err != nil {
			return err
		}
	}
	return nil
}

func (c PreviewConfig) clientTimeout() time.Duration {
	timeout, _ := parseDuration(c.ClientTimeout, defaultClientTimeout)
	return timeout
}

// settings maps the configuration, which validate already accepted; the
// application fills what is unset.
func (c PreviewConfig) settings() application.PreviewSettings {
	deadline, _ := parseDuration(c.TaskDeadline, 0)
	retention, _ := parseDuration(c.Retention, 0)
	return application.PreviewSettings{
		Limits: domain.PreviewLimits{
			OfficeSource: c.OfficeSourceBytes, RenderedPDF: c.RenderedPDFBytes, NativePDF: c.NativePDFBytes,
			Image: c.ImageBytes, Text: c.TextBytes, Markup: c.MarkupBytes,
		},
		Retention: retention,
		// An attempt outlives its request so the client timeout is what ends it.
		Rendering: applicationtask.RenderingPolicy{Attempt: c.clientTimeout() + 5*time.Second, Deadline: deadline},
	}
}
