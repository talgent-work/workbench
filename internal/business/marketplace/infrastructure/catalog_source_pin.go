package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

var gitCommit = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
var archiveDigest = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

func (s *CatalogSynchronizer) pinEntry(ctx context.Context, acquisition *catalogAcquisition, source CatalogSource, location catalogLocation, pluginRoot string, raw json.RawMessage) (json.RawMessage, error) {
	var entry map[string]json.RawMessage
	if json.Unmarshal(raw, &entry) != nil {
		return nil, errors.New("marketplace: invalid entry")
	}
	var relative string
	var origin map[string]any
	localPackage := ""
	if json.Unmarshal(entry["source"], &relative) == nil {
		if !strings.HasPrefix(relative, "./") && !strings.Contains(relative, "/") {
			relative = filepath.Join(pluginRoot, relative)
		}
		relative = filepath.Clean(relative)
		if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, "../") || strings.Contains(relative, "\\") {
			return nil, errors.New("marketplace: package path escapes catalog")
		}
		if source.Owned {
			// Materialize only a changed relative package, after its version check.
			if location.directory == "" && location.remote != "" {
				materialized, err := acquisition.git(ctx, location.remote, location.commit)
				if err != nil {
					return nil, err
				}
				location.directory = materialized.directory
			}
			localPackage = filepath.Join(location.directory, relative)
			resolved, err := filepath.EvalSymlinks(localPackage)
			if err != nil {
				return nil, err
			}
			catalogRoot, err := filepath.EvalSymlinks(location.directory)
			if err != nil {
				return nil, err
			}
			within, err := filepath.Rel(catalogRoot, resolved)
			if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
				return nil, errors.New("marketplace: package symlink escapes catalog")
			}
			localPackage = resolved
			published, err := s.publishOwnedPackage(ctx, localPackage)
			if err != nil {
				return nil, err
			}
			origin = published
			if _, err := acquisition.ownedArchive(ctx, published["url"].(string), filepath.Join(s.store.directory, "archives", published["sha256"].(string)+".zip"), published["sha256"].(string)); err != nil {
				return nil, err
			}
		} else {
			remote, commit := location.remote, location.commit
			if remote == "" {
				rawRemote, err := acquisition.command.execute(ctx, "git", "-C", location.directory, "remote", "get-url", "origin")
				if err != nil {
					return nil, errors.New("marketplace: relative third-party packages need their source git repository")
				}
				rawCommit, err := acquisition.command.execute(ctx, "git", "-C", location.directory, "rev-parse", "HEAD")
				if err != nil {
					return nil, err
				}
				remote, commit = strings.TrimSpace(string(rawRemote)), strings.TrimSpace(string(rawCommit))
			}
			origin = map[string]any{"source": "git-subdir", "url": remote, "path": relative, "sha": commit}
		}
	} else if json.Unmarshal(entry["source"], &origin) != nil {
		return nil, errors.New("marketplace: unsupported source representation")
	}
	kind, _ := origin["source"].(string)
	switch kind {
	case "github", "url", "git-subdir":
		sha, _ := origin["sha"].(string)
		if sha != "" && !gitCommit.MatchString(sha) {
			return nil, errors.New("marketplace: invalid git pin")
		}
		if sha == "" {
			remote, _ := origin["url"].(string)
			if kind == "github" {
				repository, _ := origin["repo"].(string)
				remote = "https://github.com/" + repository + ".git"
			}
			ref, _ := origin["ref"].(string)
			var err error
			sha, err = acquisition.revision(ctx, normalizeGitRemote(remote), ref)
			if err != nil {
				return nil, err
			}
			origin["sha"] = sha
		}
	case "archive":
		pin, _ := origin["sha256"].(string)
		if pin == "" {
			if len(entry["headersHelper"]) > 0 {
				return nil, errors.New("marketplace: authenticated command-based archives require an explicit sha256 pin")
			}
			address, _ := origin["url"].(string)
			var headers map[string]string
			if value, ok := entry["headers"]; ok && json.Unmarshal(value, &headers) != nil {
				return nil, errors.New("marketplace: invalid archive headers")
			}
			var err error
			var result acquisitionResult
			result, err = acquisition.archive(ctx, address, headers)
			pin = result.digest
			if err != nil {
				return nil, err
			}
			origin["sha256"] = pin
		}
		if !archiveDigest.MatchString(pin) {
			return nil, errors.New("marketplace: invalid archive digest")
		}
	case "npm":
		name, _ := origin["package"].(string)
		version, _ := origin["version"].(string)
		if name == "" || strings.HasPrefix(name, "-") {
			return nil, errors.New("marketplace: invalid npm source")
		}
		if version == "" {
			version = "latest"
		}
		args := []string{"view", name + "@" + version, "version", "--json", "--ignore-scripts"}
		if registry, ok := origin["registry"].(string); ok {
			args = append(args, "--registry", registry)
		}
		output, err := acquisition.command.execute(ctx, "npm", args...)
		if err != nil {
			return nil, err
		}
		var exact string
		if json.Unmarshal(output, &exact) != nil {
			var versions []string
			if json.Unmarshal(output, &versions) != nil || len(versions) == 0 {
				return nil, errors.New("marketplace: npm did not resolve a version")
			}
			exact = versions[len(versions)-1]
		}
		origin["version"] = exact
	case "command":
		return nil, errors.New("marketplace: command sources cannot guarantee a retained package; publish an immutable git or archive source")
	default:
		return nil, errors.New("marketplace: unsupported plugin source")
	}
	encoded, err := json.Marshal(origin)
	if err != nil {
		return nil, err
	}
	entry["source"] = encoded
	encoded, err = json.Marshal(entry)
	return encoded, err
}
