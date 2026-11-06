package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type acquisitionResult struct{ directory, file, digest, revision string }
type acquisitionCall struct {
	done   chan struct{}
	result acquisitionResult
	err    error
}
type acquisitionCalls struct {
	mu    sync.Mutex
	calls map[string]*acquisitionCall
}
type catalogAcquisition struct {
	root    string
	command catalogCommand
	http    *http.Client
	// scope keeps a private source's results apart from unauthenticated reads.
	scope  string
	shared *acquisitionCalls
}

func newCatalogAcquisition(config CatalogAcquisitionConfig) (*catalogAcquisition, error) {
	root, e := os.MkdirTemp("", "talgent-catalog-")
	if e != nil {
		return nil, e
	}
	client, e := catalogHTTPClient(config)
	if e != nil {
		os.RemoveAll(root)
		return nil, e
	}
	return &catalogAcquisition{root: root, command: catalogCommand{home: root, workspace: root, timeout: time.Duration(config.RequestTimeoutSeconds) * time.Second, certificateAuthority: config.CertificateAuthority}, http: client, shared: &acquisitionCalls{calls: map[string]*acquisitionCall{}}}, nil
}
func (a *catalogAcquisition) close() { a.http.CloseIdleConnections(); os.RemoveAll(a.root) }

// One acquisition per immutable reference, shared by all workers and sources.
// A failed acquisition is also shared: workers do not cause a retry storm.
func (a *catalogAcquisition) once(ctx context.Context, key string, fetch func() (acquisitionResult, error)) (acquisitionResult, error) {
	key = a.scope + key
	a.shared.mu.Lock()
	call, found := a.shared.calls[key]
	if !found {
		call = &acquisitionCall{done: make(chan struct{})}
		a.shared.calls[key] = call
	}
	a.shared.mu.Unlock()
	if found {
		select {
		case <-call.done:
			return call.result, call.err
		case <-ctx.Done():
			return acquisitionResult{}, ctx.Err()
		}
	}
	call.result, call.err = fetch()
	close(call.done)
	return call.result, call.err
}
func (a *catalogAcquisition) catalog(ctx context.Context, uri string) (catalogLocation, error) {
	if info, e := os.Stat(uri); e == nil {
		directory := uri
		var body []byte
		if info.IsDir() {
			body, e = readCatalogManifest(uri)
		} else {
			body, e = os.ReadFile(uri)
			directory = filepath.Dir(uri)
			if filepath.Base(directory) == ".claude-plugin" {
				directory = filepath.Dir(directory)
			}
		}
		return catalogLocation{directory: directory, manifest: body}, e
	}
	u, e := url.Parse(uri)
	if e == nil && u.Scheme == "https" && strings.HasSuffix(u.Path, ".json") {
		body, e := a.readHTTP(ctx, uri, nil, 16<<20)
		return catalogLocation{manifest: body}, e
	}
	remote, ref := uri, "HEAD"
	if e == nil && u.Fragment != "" {
		ref = u.Fragment
		u.Fragment = ""
		remote = u.String()
	}
	remote = normalizeGitRemote(remote)
	if repo, ok := githubRepository(remote); ok {
		sha, e := a.revision(ctx, remote, ref)
		if e != nil {
			return catalogLocation{}, e
		}
		body, e := a.readHTTP(ctx, "https://raw.githubusercontent.com/"+repo+"/"+sha+"/.claude-plugin/marketplace.json", nil, 16<<20)
		return catalogLocation{remote: remote, commit: sha, manifest: body}, e
	}
	release, e := a.git(ctx, remote, ref)
	if e != nil {
		return catalogLocation{}, e
	}
	body, e := readCatalogManifest(release.directory)
	return catalogLocation{directory: release.directory, remote: remote, commit: release.revision, manifest: body}, e
}
func normalizeGitRemote(remote string) string {
	if !strings.Contains(remote, ":") && strings.Count(remote, "/") == 1 {
		return "https://github.com/" + strings.TrimSuffix(remote, ".git") + ".git"
	}
	return remote
}
func (a *catalogAcquisition) revision(ctx context.Context, remote, ref string) (string, error) {
	if gitCommit.MatchString(ref) {
		return strings.ToLower(ref), nil
	}
	if ref == "" {
		ref = "HEAD"
	}
	result, e := a.once(ctx, "revision:"+remote+":"+ref, func() (acquisitionResult, error) {
		if !strings.HasPrefix(remote, "https://") && !strings.HasPrefix(remote, "git@") && !strings.HasPrefix(remote, "ssh://") {
			return acquisitionResult{}, errors.New("marketplace: invalid git source")
		}
		output, e := a.command.execute(ctx, "git", "ls-remote", "--exit-code", "--", remote, ref, ref+"^{}")
		if e != nil {
			return acquisitionResult{}, e
		}
		sha := ""
		for _, line := range strings.Split(string(output), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && gitCommit.MatchString(f[0]) {
				sha = f[0]
			}
		}
		if sha == "" {
			return acquisitionResult{}, errors.New("marketplace: cannot resolve exact git revision")
		}
		return acquisitionResult{revision: strings.ToLower(sha)}, nil
	})
	return result.revision, e
}
func githubRepository(remote string) (string, bool) {
	u, e := url.Parse(remote)
	if e != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	repo := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || parts[0] == ".." || parts[1] == ".." {
		return "", false
	}
	return repo, true
}
func (a *catalogAcquisition) git(ctx context.Context, remote, ref string) (acquisitionResult, error) {
	remote = normalizeGitRemote(remote)
	sha, e := a.revision(ctx, remote, ref)
	if e != nil {
		return acquisitionResult{}, e
	}
	return a.once(ctx, "git:"+remote+":"+sha, func() (acquisitionResult, error) {
		if repo, ok := githubRepository(remote); ok {
			result, e := a.archive(ctx, "https://codeload.github.com/"+repo+"/zip/"+sha, nil)
			if e != nil {
				return acquisitionResult{}, e
			}
			files, e := os.ReadDir(result.directory)
			if e != nil || len(files) != 1 || !files[0].IsDir() {
				return acquisitionResult{}, errors.New("marketplace: invalid repository archive root")
			}
			return acquisitionResult{directory: filepath.Join(result.directory, files[0].Name()), revision: sha}, nil
		}
		directory, e := os.MkdirTemp(a.root, "git-")
		if e != nil {
			return acquisitionResult{}, e
		}
		for _, args := range [][]string{{"-C", directory, "init", "--quiet"}, {"-C", directory, "-c", "core.hooksPath=/dev/null", "fetch", "--depth=1", "--no-tags", "--", remote, sha}, {"-C", directory, "-c", "core.hooksPath=/dev/null", "checkout", "--quiet", "--detach", "FETCH_HEAD"}} {
			if _, e = a.command.execute(ctx, "git", args...); e != nil {
				return acquisitionResult{}, e
			}
		}
		return acquisitionResult{directory: directory, revision: sha}, nil
	})
}
func (a *catalogAcquisition) archive(ctx context.Context, address string, headers map[string]string) (acquisitionResult, error) {
	encoded, _ := json.Marshal(headers)
	return a.once(ctx, "archive:"+address+":"+string(encoded), func() (acquisitionResult, error) {
		file, e := os.CreateTemp(a.root, "archive-*.zip")
		if e != nil {
			return acquisitionResult{}, e
		}
		defer file.Close()
		hash := sha256.New()
		if e = a.download(ctx, address, headers, file, hash); e != nil {
			return acquisitionResult{}, e
		}
		directory, e := os.MkdirTemp(a.root, "package-")
		if e != nil {
			return acquisitionResult{}, e
		}
		if e = extractCatalogZIP(ctx, file.Name(), directory); e != nil {
			return acquisitionResult{}, e
		}
		return acquisitionResult{file: file.Name(), directory: directory, digest: hex.EncodeToString(hash.Sum(nil))}, nil
	})
}
func packageDirectory(root, subdir string) (string, error) {
	if subdir == "" {
		return root, nil
	}
	if strings.Contains(subdir, "\\") || !filepath.IsLocal(subdir) {
		return "", errors.New("marketplace: package path escapes catalog")
	}
	resolved, e := filepath.EvalSymlinks(filepath.Join(root, subdir))
	if e != nil {
		return "", e
	}
	relative, e := filepath.Rel(root, resolved)
	if e != nil || !filepath.IsLocal(relative) {
		return "", errors.New("marketplace: package symlink escapes catalog")
	}
	return resolved, nil
}
func (a *catalogAcquisition) plugin(ctx context.Context, entry []byte) (string, error) {
	var v struct {
		Source struct {
			Kind     string `json:"source"`
			URL      string `json:"url"`
			Repo     string `json:"repo"`
			SHA      string `json:"sha"`
			SHA256   string `json:"sha256"`
			Path     string `json:"path"`
			Package  string `json:"package"`
			Version  string `json:"version"`
			Registry string `json:"registry"`
		} `json:"source"`
		Headers       map[string]string `json:"headers"`
		HeadersHelper string            `json:"headersHelper"`
	}
	if e := json.Unmarshal(entry, &v); e != nil {
		return "", e
	}
	if v.HeadersHelper != "" {
		return "", errors.New("marketplace: command-based download headers are unsupported")
	}
	origin := v.Source
	switch origin.Kind {
	case "github", "url", "git-subdir":
		remote := origin.URL
		if origin.Kind == "github" {
			remote = "https://github.com/" + origin.Repo + ".git"
		}
		r, e := a.git(ctx, remote, origin.SHA)
		if e != nil {
			return "", e
		}
		return packageDirectory(r.directory, origin.Path)
	case "archive":
		r, e := a.archive(ctx, origin.URL, v.Headers)
		if e != nil {
			return "", e
		}
		if !strings.EqualFold(origin.SHA256, r.digest) {
			return "", errors.New("marketplace: archive checksum mismatch")
		}
		return archivePluginRoot(r.directory)
	case "npm":
		return a.npm(ctx, origin.Package, origin.Version, origin.Registry)
	default:
		return "", errors.New("marketplace: unsupported plugin source")
	}
}
