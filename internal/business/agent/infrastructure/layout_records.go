package infrastructure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// workLayoutRecord is agent_definition.work_layout.
type workLayoutRecord struct {
	Mounts []layoutMountRecord `json:"mounts"`
}

type layoutMountRecord struct {
	Path         string          `json:"path,omitempty"`
	ReadOnly     bool            `json:"read_only"`
	HarnessState bool            `json:"harness_state,omitempty"`
	Locators     []locatorRecord `json:"locators"`
}

type locatorRecord struct {
	Conditions []string `json:"conditions"`
	Name       string   `json:"name,omitempty"`
	Backend    string   `json:"backend"`
	Lifetime   string   `json:"lifetime"`
}

// locatedLayoutRecord is agent_session.located_layout.
type locatedLayoutRecord struct {
	Mounts            []locatedMountRecord    `json:"mounts"`
	Inputs            *locatedDirectoryRecord `json:"inputs,omitempty"`
	PublicationTarget locatedDirectoryRecord  `json:"publication_target"`
}

type locatedMountRecord struct {
	Path      string                 `json:"path"`
	ReadOnly  bool                   `json:"read_only"`
	Directory locatedDirectoryRecord `json:"directory"`
}

type locatedDirectoryRecord struct {
	Owner    []ownerFactRecord `json:"owner"`
	Name     string            `json:"name"`
	Backend  string            `json:"backend"`
	Lifetime string            `json:"lifetime"`
}

type ownerFactRecord struct {
	Condition string `json:"condition"`
	Value     string `json:"value"`
}

func encodeWorkLayout(layout domain.WorkLayout) (string, error) {
	record := workLayoutRecord{Mounts: make([]layoutMountRecord, 0, len(layout.Mounts))}
	for _, mount := range layout.Mounts {
		mounted := layoutMountRecord{Path: mount.Path, ReadOnly: mount.ReadOnly, HarnessState: mount.HarnessState}
		for _, locator := range mount.Locators {
			conditions := make([]string, 0, len(locator.Conditions))
			for _, condition := range locator.Conditions {
				conditions = append(conditions, string(condition))
			}
			mounted.Locators = append(mounted.Locators, locatorRecord{Conditions: conditions, Name: locator.Name, Backend: string(locator.Backend), Lifetime: string(locator.Lifetime)})
		}
		record.Mounts = append(record.Mounts, mounted)
	}
	return encode(record)
}

func decodeWorkLayout(value string) (domain.WorkLayout, error) {
	var record workLayoutRecord
	if err := json.Unmarshal([]byte(value), &record); err != nil {
		return domain.WorkLayout{}, oops.Wrap(err)
	}
	layout := domain.WorkLayout{Mounts: make([]domain.LayoutMount, 0, len(record.Mounts))}
	for _, mounted := range record.Mounts {
		mount := domain.LayoutMount{Path: mounted.Path, ReadOnly: mounted.ReadOnly, HarnessState: mounted.HarnessState}
		for _, locator := range mounted.Locators {
			conditions := make([]domain.LocatorCondition, 0, len(locator.Conditions))
			for _, condition := range locator.Conditions {
				conditions = append(conditions, domain.LocatorCondition(condition))
			}
			mount.Locators = append(mount.Locators, domain.DirectoryLocator{Conditions: conditions, Name: locator.Name, Backend: domain.DirectoryBackend(locator.Backend), Lifetime: domain.DirectoryLifetime(locator.Lifetime)})
		}
		layout.Mounts = append(layout.Mounts, mount)
	}
	return layout, nil
}

func locatedDirectoryToRecord(directory domain.LocatedDirectory) locatedDirectoryRecord {
	return locatedDirectoryRecord{Owner: ownerToRecord(directory.Directory.Owner), Name: directory.Directory.Name, Backend: string(directory.Backend), Lifetime: string(directory.Lifetime)}
}

func ownerToRecord(owner []domain.OwnerFact) []ownerFactRecord {
	facts := make([]ownerFactRecord, 0, len(owner))
	for _, fact := range owner {
		facts = append(facts, ownerFactRecord{Condition: string(fact.Condition), Value: fact.Value})
	}
	return facts
}

func (r locatedDirectoryRecord) directory() domain.Directory {
	directory := domain.Directory{Name: r.Name}
	for _, fact := range r.Owner {
		directory.Owner = append(directory.Owner, domain.OwnerFact{Condition: domain.LocatorCondition(fact.Condition), Value: fact.Value})
	}
	return directory
}

func (r locatedDirectoryRecord) located() domain.LocatedDirectory {
	return domain.LocatedDirectory{Directory: r.directory(), Backend: domain.DirectoryBackend(r.Backend), Lifetime: domain.DirectoryLifetime(r.Lifetime)}
}

func encodeLocatedLayout(layout domain.LocatedLayout) (string, error) {
	record := locatedLayoutRecord{Mounts: make([]locatedMountRecord, 0, len(layout.Mounts)), PublicationTarget: locatedDirectoryToRecord(layout.PublicationTarget)}
	for _, mount := range layout.Mounts {
		record.Mounts = append(record.Mounts, locatedMountRecord{Path: mount.Path, ReadOnly: mount.ReadOnly, Directory: locatedDirectoryToRecord(mount.Directory)})
	}
	if layout.Inputs != nil {
		inputs := locatedDirectoryToRecord(*layout.Inputs)
		record.Inputs = &inputs
	}
	return encode(record)
}

func decodeLocatedLayout(value string) (domain.LocatedLayout, error) {
	var record locatedLayoutRecord
	if err := json.Unmarshal([]byte(value), &record); err != nil {
		return domain.LocatedLayout{}, oops.Wrap(err)
	}
	return record.layout(), nil
}

func (r locatedLayoutRecord) layout() domain.LocatedLayout {
	layout := domain.LocatedLayout{PublicationTarget: r.PublicationTarget.located()}
	for _, mount := range r.Mounts {
		layout.Mounts = append(layout.Mounts, domain.LocatedMount{Path: mount.Path, ReadOnly: mount.ReadOnly, Directory: mount.Directory.located()})
	}
	if r.Inputs != nil {
		inputs := r.Inputs.located()
		layout.Inputs = &inputs
	}
	return layout
}

// DirectoryKey is the fixed-length key a Directory is indexed by: the
// SHA-256 of its owner facts, in condition order, and its name.
func DirectoryKey(directory domain.Directory) string {
	// A struct of strings always encodes, so json.Marshal has no error to
	// report here.
	encoded, _ := json.Marshal(struct {
		Owner []ownerFactRecord `json:"owner"`
		Name  string            `json:"name"`
	}{ownerToRecord(directory.Owner), directory.Name})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
