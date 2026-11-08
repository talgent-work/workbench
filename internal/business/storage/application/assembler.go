package application

import "github.com/talgent-work/workbench/internal/business/storage/domain"

// Mapping between Storage's Domain state and its Application results.

func (a *Application) directoryFile(directory *domain.Directory, stored StoredFile) DirectoryFile {
	file := DirectoryFile{StoredFile: stored}
	for _, link := range directory.ActiveShareLinks(stored.Position) {
		file.ShareLinks = append(file.ShareLinks, a.shareLinkView(link))
	}
	return file
}

func (a *Application) shareLinkView(link *domain.ShareLink) ShareLinkView {
	return ShareLinkView{Token: link.Token, URL: a.links.ShareURL(link.Token), CreatedBy: link.CreatedBy, CreatedAt: link.CreatedAt}
}

// AssembleNamed is the Directory identity as a caller names it.
func AssembleNamed(id domain.DirectoryID) Named {
	return Named{Owner: id.Owner.Facts(), Name: id.Name}
}
