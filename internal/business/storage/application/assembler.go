package application

import "github.com/talgent-work/workbench/internal/business/storage/domain"

// Mapping between Storage's Domain state and its Application results.

func (a *Application) spaceFile(space *domain.FileSpace, stored StoredFile) SpaceFile {
	file := SpaceFile{StoredFile: stored}
	for _, link := range space.ActiveShareLinks(stored.Position) {
		file.ShareLinks = append(file.ShareLinks, a.shareLinkView(link))
	}
	return file
}

func (a *Application) shareLinkView(link *domain.ShareLink) ShareLinkView {
	return ShareLinkView{Token: link.Token, URL: a.links.ShareURL(link.Token), CreatedBy: link.CreatedBy, CreatedAt: link.CreatedAt}
}
