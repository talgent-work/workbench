package lifecyclelog_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/pkg/lifecyclelog"
)

func stopFirst(lc fx.Lifecycle) { lc.Append(fx.StopHook(func() {})) }
func stopSecond(lc fx.Lifecycle) {
	lc.Append(fx.StopHook(func() error { return errors.New("close failed") }))
}

// Each stop hook is reported in the order it ran, a failing one as a warning,
// and nothing is reported while the process starts cleanly.
func TestReportsStopHooksInStopOrder(t *testing.T) {
	var out bytes.Buffer
	app := fx.New(
		fx.Supply(slog.New(slog.NewTextHandler(&out, nil))),
		lifecyclelog.Option,
		fx.Invoke(stopFirst),
		fx.Invoke(stopSecond),
	)
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("start logged %q, want nothing", out.String())
	}
	if err := app.Stop(context.Background()); err == nil {
		t.Fatal("stop: want the failing hook's error")
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("logged %d lines, want 2:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], "level=WARN") || !strings.Contains(lines[0], "stop hook failed") || !strings.Contains(lines[0], "stopSecond") || !strings.Contains(lines[0], "close failed") {
		t.Errorf("first line = %q, want the later-registered hook failing", lines[0])
	}
	if !strings.Contains(lines[1], "stop hook done") || !strings.Contains(lines[1], "stopFirst") {
		t.Errorf("second line = %q, want the earlier-registered hook done", lines[1])
	}
}

type missing struct{}

func needsMissing(missing) {}

// A process that cannot assemble its dependencies says why before it exits.
func TestReportsAStartThatCannotAssemble(t *testing.T) {
	var out bytes.Buffer
	app := fx.New(
		fx.Supply(slog.New(slog.NewTextHandler(&out, nil))),
		lifecyclelog.Option,
		fx.Invoke(needsMissing),
	)
	if err := app.Start(context.Background()); err == nil {
		t.Fatal("start: want the missing dependency's error")
	}
	if !strings.Contains(out.String(), "level=ERROR") || !strings.Contains(out.String(), "start failed") || !strings.Contains(out.String(), "lifecyclelog_test.missing") {
		t.Fatalf("logged %q, want the missing dependency named", out.String())
	}
}
