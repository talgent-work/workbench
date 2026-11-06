package query_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type fixedView struct{ value query.WorkView }

func (f fixedView) GetWorkView(context.Context, string) (query.WorkView, error) { return f.value, nil }

func TestWorkViewsPresentTheRetainedModelConfigurationAsCurrentlyProvided(t *testing.T) {
	glm := query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "glm"}, Name: "GLM 5.3", Vendor: "zhipu", ThinkingLevels: []string{"low", "high", "max"}}
	models := query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deepseek"}, Name: "DeepSeek Flash", Vendor: "deepseek"}, glm)
	first, ok := models.Default()
	require.True(t, ok)
	require.Equal(t, "deepseek", first.Selection.ConfigurationID, "the first provided configuration is the default")

	view, err := query.NewModelOptionViews(fixedView{query.WorkView{WorkModel: glm.Selection}}, models).GetWorkView(t.Context(), "work")
	require.NoError(t, err)
	require.Equal(t, &glm, view.ModelOption)

	view, err = query.NewModelOptionViews(fixedView{query.WorkView{WorkModel: domain.WorkModelSelection{ConfigurationID: "withdrawn"}}}, models).GetWorkView(t.Context(), "work")
	require.NoError(t, err)
	require.Nil(t, view.ModelOption, "a withdrawn configuration has no current option")

	provided := models.Provided()
	provided[1].ThinkingLevels[0] = "changed"
	again, _ := models.Option(glm.Selection)
	require.Equal(t, "low", again.ThinkingLevels[0], "callers cannot change the provided configurations")
}
