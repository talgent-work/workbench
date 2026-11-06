package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type packageIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Read only the authoritative package identity. For GitHub this avoids
// downloading a repository archive merely to discover an unchanged version.
func (a *catalogAcquisition) manifestIdentity(ctx context.Context, location catalogLocation, pluginRoot string, entry []byte) (packageIdentity, error) {
	var v struct {
		Source json.RawMessage `json:"source"`
	}
	if e := json.Unmarshal(entry, &v); e != nil {
		return packageIdentity{}, e
	}
	var relative string
	if json.Unmarshal(v.Source, &relative) == nil {
		if !strings.HasPrefix(relative, "./") && !strings.Contains(relative, "/") {
			relative = filepath.Join(pluginRoot, relative)
		}
		if location.remote != "" && location.directory == "" {
			return a.gitManifestIdentity(ctx, location.remote, location.commit, relative)
		}
		directory, e := packageDirectory(location.directory, relative)
		if e != nil {
			return packageIdentity{}, e
		}
		return readPackageIdentity(directory)
	}
	var origin map[string]any
	if e := json.Unmarshal(v.Source, &origin); e != nil {
		return packageIdentity{}, e
	}
	kind, _ := origin["source"].(string)
	if kind == "github" || kind == "url" || kind == "git-subdir" {
		remote, _ := origin["url"].(string)
		if kind == "github" {
			repo, _ := origin["repo"].(string)
			remote = "https://github.com/" + repo + ".git"
		}
		remote = normalizeGitRemote(remote)
		ref, _ := origin["sha"].(string)
		if ref == "" {
			ref, _ = origin["ref"].(string)
		}
		sha, e := a.revision(ctx, remote, ref)
		if e != nil {
			return packageIdentity{}, e
		}
		subdir, _ := origin["path"].(string)
		if subdir != "" && (!filepath.IsLocal(subdir) || strings.Contains(subdir, "\\")) {
			return packageIdentity{}, errors.New("marketplace: package path escapes repository")
		}
		return a.gitManifestIdentity(ctx, remote, sha, subdir)
	}
	if kind == "archive" {
		address, _ := origin["url"].(string)
		var headers struct {
			Headers map[string]string `json:"headers"`
			Helper  string            `json:"headersHelper"`
		}
		if e := json.Unmarshal(entry, &headers); e != nil {
			return packageIdentity{}, e
		}
		if headers.Helper != "" {
			return packageIdentity{}, errors.New("marketplace: command-based download headers are unsupported")
		}
		result, e := a.archive(ctx, address, headers.Headers)
		if e != nil {
			return packageIdentity{}, e
		}
		if digest, _ := origin["sha256"].(string); digest != "" && !strings.EqualFold(digest, result.digest) {
			return packageIdentity{}, errors.New("marketplace: archive checksum mismatch")
		}
		directory, e := archivePluginRoot(result.directory)
		if e != nil {
			return packageIdentity{}, e
		}
		return readPackageIdentity(directory)
	}
	if kind == "npm" {
		name, _ := origin["package"].(string)
		version, _ := origin["version"].(string)
		registry, _ := origin["registry"].(string)
		directory, e := a.npm(ctx, name, version, registry)
		if e != nil {
			return packageIdentity{}, e
		}
		return readPackageIdentity(directory)
	}
	return packageIdentity{}, errors.New("marketplace: unsupported plugin source")
}
func readPackageIdentity(directory string) (packageIdentity, error) {
	root, e := os.OpenRoot(directory)
	if e != nil {
		return packageIdentity{}, e
	}
	defer root.Close()
	body, e := readMetadata(root.FS(), ".claude-plugin/plugin.json")
	if errors.Is(e, os.ErrNotExist) {
		return packageIdentity{}, nil
	}
	if e != nil {
		return packageIdentity{}, e
	}
	return identityFromManifest(body)
}
func identityFromManifest(body []byte) (packageIdentity, error) {
	var v packageIdentity
	if e := json.Unmarshal(body, &v); e != nil {
		return packageIdentity{}, errors.New("marketplace: invalid plugin manifest")
	}
	return v, nil
}
func (a *catalogAcquisition) ownedArchive(ctx context.Context, address, file, digest string) (acquisitionResult, error) {
	return a.once(ctx, "archive:"+address+":null", func() (acquisitionResult, error) {
		directory, e := os.MkdirTemp(a.root, "owned-")
		if e != nil {
			return acquisitionResult{}, e
		}
		if e = extractCatalogZIP(ctx, file, directory); e != nil {
			return acquisitionResult{}, e
		}
		return acquisitionResult{file: file, directory: directory, digest: digest}, nil
	})
}
func (a *catalogAcquisition) npm(ctx context.Context, name, version, registry string) (string, error) {
	if name == "" || strings.HasPrefix(name, "-") {
		return "", errors.New("marketplace: invalid npm package")
	}
	if version == "" {
		version = "latest"
	}
	result, e := a.once(ctx, "npm:"+registry+":"+name+"@"+version, func() (acquisitionResult, error) {
		directory, e := os.MkdirTemp(a.root, "npm-")
		if e != nil {
			return acquisitionResult{}, e
		}
		args := []string{"pack", name + "@" + version, "--ignore-scripts", "--json", "--pack-destination", directory}
		if registry != "" {
			args = append(args, "--registry", registry)
		}
		output, e := a.command.execute(ctx, "npm", args...)
		if e != nil {
			return acquisitionResult{}, e
		}
		var packs []struct {
			Filename string `json:"filename"`
		}
		if json.Unmarshal(output, &packs) != nil || len(packs) != 1 || filepath.Base(packs[0].Filename) != packs[0].Filename {
			return acquisitionResult{}, errors.New("marketplace: invalid npm package result")
		}
		file := filepath.Join(directory, packs[0].Filename)
		info, e := os.Stat(file)
		if e != nil {
			return acquisitionResult{}, e
		}
		if info.Size() > catalogArchiveLimit {
			return acquisitionResult{}, errors.New("marketplace: package download exceeds limit")
		}
		if e = extractCatalogTarGzip(ctx, file, directory); e != nil {
			return acquisitionResult{}, e
		}
		return acquisitionResult{directory: filepath.Join(directory, "package")}, nil
	})
	return result.directory, e
}

func (a *catalogAcquisition) gitManifestIdentity(ctx context.Context, remote, sha, subdir string) (packageIdentity, error) {
	if subdir != "" && (!filepath.IsLocal(subdir) || strings.Contains(subdir, "\\")) {
		return packageIdentity{}, errors.New("marketplace: package path escapes repository")
	}
	if repo, ok := githubRepository(remote); ok {
		body, e := a.readHTTP(ctx, "https://raw.githubusercontent.com/"+repo+"/"+sha+"/"+path.Join(subdir, ".claude-plugin/plugin.json"), nil, maxMetadataFile)
		if errors.Is(e, os.ErrNotExist) {
			return packageIdentity{}, nil
		}
		if e != nil {
			return packageIdentity{}, e
		}
		return identityFromManifest(body)
	}
	result, e := a.git(ctx, remote, sha)
	if e != nil {
		return packageIdentity{}, e
	}
	directory, e := packageDirectory(result.directory, subdir)
	if e != nil {
		return packageIdentity{}, e
	}
	return readPackageIdentity(directory)
}
