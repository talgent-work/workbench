package application

import (
	"errors"
	"testing"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// headInspector reports the leading bytes it was handed as the content.
type headInspector struct{ detected string }

func (i headInspector) InspectHead(head []byte) domain.ContentStructure {
	return domain.ContentStructure{Head: head, DetectedType: i.detected}
}

// A file Storage does not hold is judged as a Session Workspace file under
// Storage's configured limits, with the most bytes its reader is served.
func TestJudgeFilePreviewUsesStoragesWorkspaceRulesAndLimits(t *testing.T) {
	limits := domain.PreviewLimits{Text: 100}
	app := NewApplication(&fakeSpaces{}, Ports{Inspector: headInspector{detected: "image/png"}}, LinkLifetimes{}, PreviewSettings{Limits: limits}, WorkspaceSettings{}, nil)

	judged, err := app.JudgeFilePreview("out/notes.md", 101, []byte("# hi"))
	if err != nil || judged.Verdict != (domain.PreviewVerdict{Mode: domain.PreviewTruncated, Display: domain.PreviewDisplayText, LimitBytes: 100}) || judged.ReadLimit != 100 {
		t.Fatalf("a long text file: %+v, %v", judged, err)
	}
	judged, err = app.JudgeFilePreview("shot.png", 8, []byte("\x89PNG"))
	if err != nil || judged.Verdict.Mode != domain.PreviewDirect || judged.ReadLimit != domain.DefaultPreviewLimits().Image {
		t.Fatalf("an image: %+v, %v", judged, err)
	}
	judged, err = app.JudgeFilePreview("report.docx", 4, []byte("PK\x03\x04"))
	if err != nil || judged.Verdict.Refusal != domain.PreviewRefusalUnsupported || judged.ReadLimit != 0 {
		t.Fatalf("an office file has no Preview Copy here: %+v, %v", judged, err)
	}
	for name, call := range map[string]func() (JudgedPreview, error){
		"no name":         func() (JudgedPreview, error) { return app.JudgeFilePreview(" ", 1, nil) },
		"a negative size": func() (JudgedPreview, error) { return app.JudgeFilePreview("a.txt", -1, nil) },
		"a head past the probe": func() (JudgedPreview, error) {
			return app.JudgeFilePreview("a.txt", 1<<20, make([]byte, domain.TextProbeBytes+1))
		},
	} {
		if _, err := call(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
