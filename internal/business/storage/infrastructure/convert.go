package infrastructure

import (
	"time"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

func fileSpaceToDO(space *domain.FileSpace) *fileSpaceDO {
	return &fileSpaceDO{Owner: space.Owner, SpaceToken: space.SpaceToken, Version: space.Version}
}

func shareLinkToDO(owner string, link *domain.ShareLink) *shareLinkDO {
	return &shareLinkDO{
		Token: link.Token, Owner: owner, TargetKind: string(link.Target.Kind), TargetName: link.Target.Name,
		CreatedBy: link.CreatedBy, State: string(link.State), CreatedAt: link.CreatedAt.UnixMilli(),
	}
}

func fileSpaceFromDO(row *fileSpaceDO, links []shareLinkDO) *domain.FileSpace {
	space := &domain.FileSpace{Owner: row.Owner, SpaceToken: row.SpaceToken, Version: row.Version}
	for _, link := range links {
		space.ShareLinks = append(space.ShareLinks, &domain.ShareLink{
			Token:     link.Token,
			Target:    domain.FilePosition{Owner: row.Owner, Kind: domain.FileKind(link.TargetKind), Name: link.TargetName},
			CreatedBy: link.CreatedBy,
			CreatedAt: time.UnixMilli(link.CreatedAt).UTC(),
			State:     domain.ShareLinkState(link.State),
		})
	}
	return space
}
