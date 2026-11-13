package query_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A trusted service selects Works only by a Work Caller of a kind an extension
// defines: a person's or an Application's Works are the owner's own, and no
// selection by them is served; nothing is read for it.
func TestTrustedSelectionsTakeOnlyAnExtensionWorkCaller(t *testing.T) {
	origins := originsFake{origins: map[string]query.WorkOrigin{"own": {WorkID: "own", WorkCaller: domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "jack"}}}}
	starts := query.NewResolveWorkStartsHandler(origins)
	positions := query.NewResolveWorkContextPositionHandler(origins, callPositions{})
	list := query.NewListWorksHandler(origins)
	report := query.NewGetWorkReportHandler(origins, time.Now)
	for _, caller := range []domain.WorkCaller{{Kind: domain.CallerKindPerson, ID: "jack"}, {Kind: domain.CallerKindApplication, ID: "app"}} {
		_, err := starts.Handle(t.Context(), caller, []string{"own"})
		require.ErrorIs(t, err, domain.ErrStandaloneWorkOwnerOnly)
		_, err = positions.Handle(t.Context(), caller, "own", "call")
		require.ErrorIs(t, err, domain.ErrStandaloneWorkOwnerOnly)
		_, err = list.Handle(t.Context(), query.ListWorks{Filter: query.WorkListFilter{WorkCaller: &caller}})
		require.ErrorIs(t, err, domain.ErrStandaloneWorkOwnerOnly)
		_, err = report.Handle(t.Context(), query.GetWorkReport{Scope: query.WorkReportScope{WorkCaller: caller}, Window: "24h"})
		require.ErrorIs(t, err, domain.ErrStandaloneWorkOwnerOnly)
	}
}
