//go:build integration

package dispatcher_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter/claudecode"
	"github.com/talgent-work/workbench/pkg/nativetest"
)

// Real pinned Claude emits the context flags; only inference is controlled.
// This detects drift that hand-written synthetic-message fixtures cannot.
func TestNativeSkillBodiesAreHiddenByContextMarkers(t *testing.T) {
	for _, invocation := range []string{"slash", "tool"} {
		t.Run(invocation, func(t *testing.T) {
			nativetest.Require(t, "CLAUDE_BINARY")
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			root := t.TempDir()
			path := filepath.Join(root, ".claude", "skills", "context-probe", "SKILL.md")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			const body = "PRIVATE_SKILL_BODY_MARKER"
			require.NoError(t, os.WriteFile(path, []byte("---\nname: context-probe\ndescription: Verify native context messages\n---\n"+body+"\n"), 0600))
			model := &nativeInference{requests: []string{"seed"}}
			prompt := "/context-probe"
			if invocation == "tool" {
				model.taskNext, model.taskTarget = "Skill", "context-probe"
				prompt = "Load the context probe skill"
			}
			h := startHarnessAt(t, ctx, model, root)
			h.input(uuid.NewString(), prompt)
			events := h.until(ctx, rootResult)
			var hidden, outcomes int
			for _, event := range events {
				raw, err := json.Marshal(event)
				require.NoError(t, err)
				messages, _, err := claudecode.UpstreamToMessages(ctx, "work", raw)
				require.NoError(t, err)
				for _, message := range messages {
					if message.GetMessage() != nil {
						for _, block := range message.GetMessage().Content {
							require.NotContains(t, block.Text, body, "native Skill body must never enter visible history")
						}
					}
					if message.Kind == dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_OBSERVATION {
						require.False(t, message.GetObservation().IsError)
						outcomes++
					}
				}
				if event["type"] == "user" && (event["isSynthetic"] == true || event["isMeta"] == true) {
					content, _ := json.Marshal(event["message"])
					if strings.Contains(string(content), body) {
						hidden++
					}
				}
			}
			if invocation == "tool" {
				require.Positive(t, hidden, "the actual CLI must mark its tool-expanded Skill body")
				require.Positive(t, outcomes, "Skill tool outcome survives filtering")
			}
			// Slash invocation omits the body from stream-json entirely, but both
			// invocation paths persist it as isMeta in the native transcript.
			var transcript []byte
			require.Eventually(t, func() bool {
				paths, err := filepath.Glob(filepath.Join(root, ".claude", "projects", "*", h.session+".jsonl"))
				if err != nil || len(paths) != 1 {
					return false
				}
				transcript, err = os.ReadFile(paths[0])
				return err == nil && strings.Contains(string(transcript), body)
			}, 5*time.Second, 50*time.Millisecond, "native transcript writer must persist the Skill body")
			metaBodies := 0
			for _, line := range strings.Split(string(transcript), "\n") {
				var event struct {
					Type    string          `json:"type"`
					IsMeta  bool            `json:"isMeta"`
					Message json.RawMessage `json:"message"`
				}
				if json.Unmarshal([]byte(line), &event) != nil || event.Type != "user" || !strings.Contains(string(event.Message), body) {
					continue
				}
				require.True(t, event.IsMeta, "persisted Skill body carries its context marker")
				messages, _, err := claudecode.UpstreamToMessages(ctx, "work", []byte(line))
				require.NoError(t, err)
				require.Empty(t, messages, "persisted Skill body must not become visible history")
				metaBodies++
			}
			require.Positive(t, metaBodies)
		})
	}
}
