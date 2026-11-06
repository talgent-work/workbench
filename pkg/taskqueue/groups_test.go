package taskqueue

import (
	"testing"

	"go.uber.org/fx"
)

// What a business module tags for a queue is what that queue's worker and
// scheduler take, and nothing tagged for another queue.
func TestGroupTagsReachTheirQueueOnly(t *testing.T) {
	var processors, periodic []string
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			fx.Annotate(func() string { return "one processor" }, fx.ResultTags(ProcessorTag("marketplace"))),
			fx.Annotate(func() []string { return []string{"two", "processors"} }, fx.ResultTags(ProcessorsTag("marketplace"))),
			fx.Annotate(func() string { return "elsewhere" }, fx.ResultTags(ProcessorTag("storage"))),
			fx.Annotate(func() string { return "one periodic" }, fx.ResultTags(PeriodicTaskTag("marketplace"))),
			fx.Annotate(func() []string { return []string{"two periodic"} }, fx.ResultTags(PeriodicTasksTag("marketplace"))),
		),
		fx.Invoke(fx.Annotate(func(p, s []string) { processors, periodic = p, s },
			fx.ParamTags(groupTag(processorGroup("marketplace"), false), groupTag(periodicTaskGroup("marketplace"), false)))),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	if len(processors) != 3 || len(periodic) != 2 {
		t.Fatalf("marketplace took processors %v and periodic tasks %v", processors, periodic)
	}
	for _, p := range processors {
		if p == "elsewhere" {
			t.Fatalf("marketplace took a processor tagged for storage: %v", processors)
		}
	}
}
