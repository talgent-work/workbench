package infrastructure

import (
	"time"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
	"github.com/talgent-work/workbench/internal/pkg/storagepath"
)

func directoryToDO(directory *domain.Directory) *directoryDO {
	work, _ := directory.ID.Owner.Work()
	return &directoryDO{
		OwnerKey: ownerKey(directory.ID.Owner), Name: directory.ID.Name, Backend: string(directory.Backend), Lifetime: string(directory.Lifetime),
		State: string(directory.State), SpaceToken: directory.SpaceToken, GrantedReadWrite: grantedToDO(directory.GrantedReadWrite), WorkID: work, Version: directory.Version,
	}
}

func grantedToDO(granted bool) uint8 {
	if granted {
		return 1
	}
	return 0
}

func shareLinkToDO(id domain.DirectoryID, link *domain.ShareLink) *shareLinkDO {
	return &shareLinkDO{
		Token: link.Token, OwnerKey: ownerKey(id.Owner), DirectoryName: id.Name, TargetName: link.Target.Name,
		CreatedBy: link.CreatedBy, State: string(link.State), CreatedAt: link.CreatedAt.UnixMilli(),
	}
}

// directoryIDFromDO reads a Directory's identity from its stored owner key
// and name.
func directoryIDFromDO(key, name string) (domain.DirectoryID, error) {
	written, err := storagepath.ParseOwner(key)
	if err != nil {
		return domain.DirectoryID{}, oops.With("owner_key", key).Wrap(err)
	}
	facts := make([]domain.OwnerFact, 0, len(written))
	for _, fact := range written {
		facts = append(facts, domain.OwnerFact{Condition: fact.Condition, Value: fact.Value})
	}
	owner, err := domain.NewDirectoryOwner(facts)
	if err != nil {
		return domain.DirectoryID{}, oops.With("owner_key", key).Wrap(err)
	}
	return domain.NewDirectoryID(owner, name)
}

func directoryFromDO(row *directoryDO, links []shareLinkDO, mappings []mappingDO) (*domain.Directory, error) {
	id, err := directoryIDFromDO(row.OwnerKey, row.Name)
	if err != nil {
		return nil, err
	}
	directory := &domain.Directory{
		ID: id, Backend: domain.Backend(row.Backend), Lifetime: domain.Lifetime(row.Lifetime), State: domain.DirectoryState(row.State),
		SpaceToken: row.SpaceToken, GrantedReadWrite: row.GrantedReadWrite == 1, Version: row.Version,
	}
	for _, mapping := range mappings {
		path, err := domain.ParseMappedPath(mapping.Path)
		if err != nil {
			return nil, oops.With("mapped_path", mapping.Path).Wrap(err)
		}
		directory.Mappings = append(directory.Mappings, path)
	}
	for _, link := range links {
		directory.ShareLinks = append(directory.ShareLinks, &domain.ShareLink{
			Token:     link.Token,
			Target:    domain.FilePosition{Directory: id, Name: link.TargetName},
			CreatedBy: link.CreatedBy,
			CreatedAt: time.UnixMilli(link.CreatedAt).UTC(),
			State:     domain.ShareLinkState(link.State),
		})
	}
	return directory, nil
}
