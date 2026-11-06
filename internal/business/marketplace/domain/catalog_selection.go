package domain

// CatalogCandidate is a supplied source configuration fact. Selecting a source
// does not create a Marketplace or Plugin aggregate or combine release content.
type CatalogCandidate struct {
	Slug     string
	Source   string
	Priority int
	Owned    bool
}

type PluginSourceSelection struct{}

func (PluginSourceSelection) SelectSource(candidates []CatalogCandidate) map[string]int {
	selected := make(map[string]int)
	for index, candidate := range candidates {
		previous, exists := selected[candidate.Slug]
		if !exists {
			selected[candidate.Slug] = index
			continue
		}
		current := candidates[previous]
		if candidate.Owned != current.Owned {
			if candidate.Owned {
				selected[candidate.Slug] = index
			}
			continue
		}
		if candidate.Priority > current.Priority || (candidate.Priority == current.Priority && candidate.Source < current.Source) {
			selected[candidate.Slug] = index
		}
	}
	return selected
}
