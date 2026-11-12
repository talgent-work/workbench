package query

import (
	"context"
	"slices"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ModelVendors are the Model Vendors the platform knows how to present. A new
// vendor needs a presentation asset, so it is added here rather than in data.
var ModelVendors = []string{"anthropic", "openai", "deepseek", "zhipu", "kimi", "qwen", "minimax"}

// ModelConfigurations are the Model Configurations the platform currently
// provides, in platform order, each with its Model Template's presentation and
// thinking levels. It is the supplied fact behind model choice and admission.
type ModelConfigurations struct {
	provided []WorkModelOption
}

func NewModelConfigurations(options ...WorkModelOption) ModelConfigurations {
	value := ModelConfigurations{provided: slices.Clone(options)}
	for i := range value.provided {
		value.provided[i].ThinkingLevels = slices.Clone(value.provided[i].ThinkingLevels)
	}
	return value
}

// Provided lists the configurations in platform order.
func (m ModelConfigurations) Provided() []WorkModelOption {
	return NewModelConfigurations(m.provided...).provided
}

// Default is the first provided configuration.
func (m ModelConfigurations) Default() (WorkModelOption, bool) {
	if len(m.provided) == 0 {
		return WorkModelOption{}, false
	}
	return m.Provided()[0], true
}

// Option finds a provided configuration; a withdrawn one has none.
func (m ModelConfigurations) Option(selection domain.WorkModelSelection) (WorkModelOption, bool) {
	for _, option := range m.Provided() {
		if option.Selection == selection {
			return option, true
		}
	}
	return WorkModelOption{}, false
}

// ThinkingLevels are the thinking levels a Work on the selection may use and
// its default: its Model Template's while its Model Configuration is
// provided, and every Harness level with no default on the machine's Claude
// login, which has no Model Template. ok is false for a withdrawn one.
func (m ModelConfigurations) ThinkingLevels(selection domain.WorkModelSelection) (levels []string, defaultLevel string, ok bool) {
	if selection.MachineLogin {
		return slices.Clone(domain.ThinkingLevels), "", true
	}
	option, ok := m.Option(selection)
	return option.ThinkingLevels, option.DefaultThinkingLevel, ok
}

// Offer is what a default model offers: whether it is provided now and its
// Model Template's thinking levels. A zero selection asks about the first
// provided configuration, the one a Work takes when its definition names no
// default model.
func (m ModelConfigurations) Offer(selection domain.WorkModelSelection) domain.ModelOffer {
	if selection == (domain.WorkModelSelection{}) {
		first, ok := m.Default()
		return domain.ModelOffer{Provided: ok, ThinkingLevels: first.ThinkingLevels}
	}
	option, ok := m.Option(selection)
	return domain.ModelOffer{Provided: ok, ThinkingLevels: option.ThinkingLevels}
}

// modelOptionViews adds the current option of each Work's retained Model
// Configuration to its view, so every reader presents the same model; the
// repository's other reads pass through.
type modelOptionViews struct {
	WorkQueryRepository
	models ModelConfigurations
}

func NewModelOptionViews(repository WorkQueryRepository, models ModelConfigurations) WorkQueryRepository {
	return modelOptionViews{WorkQueryRepository: repository, models: models}
}

func (v modelOptionViews) GetWorkView(ctx context.Context, id string) (WorkView, error) {
	value, err := v.WorkQueryRepository.GetWorkView(ctx, id)
	if err != nil {
		return WorkView{}, err
	}
	if option, ok := v.models.Option(value.WorkModel); ok {
		value.ModelOption = &option
	}
	return value, nil
}
