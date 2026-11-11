//go:build integration

package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
	"github.com/talgent-work/workbench/internal/pkg/storageerror"
)

// requireSameFile asserts that the view shows the Directory's file itself,
// with the attributes Storage recorded on it.
func requireSameFile(t *testing.T, original, shown, uploader string) {
	t.Helper()
	originalInfo, err := os.Stat(original)
	require.NoError(t, err)
	shownInfo, err := os.Lstat(shown)
	require.NoError(t, err)
	require.True(t, shownInfo.Mode().IsRegular())
	require.True(t, os.SameFile(originalInfo, shownInfo), "a mapping shows the same file, never a copy")
	buf := make([]byte, 64)
	n, err := unix.Lgetxattr(shown, "user.talgent.uploader", buf)
	require.NoError(t, err)
	require.Equal(t, uploader, string(buf[:n]))
}

// A Directory mapped at a path shows its files there as the same files;
// mapping again changes nothing, and unmapping removes the files and every
// folder it emptied, also when repeated.
func TestAMappingShowsTheDirectorysFilesAsTheSameFiles(t *testing.T) {
	f := newFixture(t)
	inputs := fileSpace(named("inputs", "subject", "case:1"))
	token := f.open(inputs)
	require.Equal(t, 200, f.upload(inputs.Directory, token, "brief.md", "brief", false).StatusCode)

	require.NoError(t, f.mapAt(inputs, "view/p1/case-1/inputs"))
	requireSameFile(t, filepath.Join(f.folder(inputs), "brief.md"), filepath.Join(f.viewOf("view/p1/case-1/inputs"), "brief.md"), "user-1")
	require.NoError(t, f.mapAt(inputs, "view/p1/case-1/inputs"), "mapping again changes nothing")
	entries, err := os.ReadDir(f.viewOf("view/p1/case-1/inputs"))
	require.NoError(t, err)
	require.Len(t, entries, 1)

	other := fileSpace(named("artifacts", "subject", "case:1"))
	require.NoError(t, f.mapAt(other, "view/p1/case-1/artifacts"))

	require.NoError(t, f.app.UnmapDirectory(t.Context(), inputs.Directory, "view/p1/case-1/inputs", viewPrefix))
	require.NoDirExists(t, f.viewOf("view/p1/case-1/inputs"))
	require.DirExists(t, f.viewOf("view/p1/case-1"), "a folder another mapping still holds stays")
	require.FileExists(t, filepath.Join(f.folder(inputs), "brief.md"), "the Directory keeps its files")
	require.NoError(t, f.app.UnmapDirectory(t.Context(), inputs.Directory, "view/p1/case-1/inputs", viewPrefix), "unmapping again changes nothing")

	require.NoError(t, f.app.UnmapDirectory(t.Context(), other.Directory, "view/p1/case-1/artifacts", viewPrefix))
	require.NoDirExists(t, f.viewOf("view/p1"), "every folder the last mapping emptied goes")
	require.NoError(t, f.mapAt(inputs, "view/p1/case-1/inputs"), "an ended mapping's path is free again")
}

// Mapping a Directory that was never opened opens it, and its place in the
// view exists before it has files.
func TestAnUnopenedDirectoryIsOpenedAndMapped(t *testing.T) {
	f := newFixture(t)
	inputs := fileSpace(named("inputs", "subject", "case:2"))
	require.NoError(t, f.mapAt(inputs, "view/p1/case-2/inputs"))
	require.DirExists(t, f.viewOf("view/p1/case-2/inputs"))
	require.DirExists(t, f.folder(inputs), "its space was allocated")
	token := f.open(inputs)
	require.Equal(t, 200, f.upload(inputs.Directory, token, "late.md", "late", false).StatusCode)
	require.NoError(t, f.app.UnmapDirectory(t.Context(), named("inputs", "subject", "never"), "view/p1/never/inputs", viewPrefix),
		"unmapping a Directory never opened changes nothing")
}

// A Directory once granted read-write is never mapped, and a mapped one is
// never granted read-write, each refused with its own reason.
func TestReadWriteGrantsAndMappingsExcludeEachOther(t *testing.T) {
	f := newFixture(t)
	written := fileSpace(named("outputs", "subject", "case:3"))
	_, err := f.app.AcquireMounts(t.Context(), "work-1", []application.MountRequest{{Directory: written}})
	require.NoError(t, err)
	err = f.mapAt(written, "view/p1/case-3/outputs")
	require.ErrorIs(t, err, domain.ErrGrantedReadWrite)
	require.NoDirExists(t, f.viewOf("view/p1/case-3/outputs"))

	shown := fileSpace(named("inputs", "subject", "case:3"))
	require.NoError(t, f.mapAt(shown, "view/p1/case-3/inputs"))
	_, err = f.app.AcquireMounts(t.Context(), "work-1", []application.MountRequest{{Directory: shown}})
	require.ErrorIs(t, err, domain.ErrDirectoryMapped)
	_, err = f.mounts.AcquireMounts(f.as("a"), &storageprivatev1.AcquireMountsRequest{WorkId: "work-1", Mounts: []*storageprivatev1.MountRequest{{Directory: locatedProto(shown)}}})
	require.Equal(t, storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_MAPPED, storageerror.ReasonOf(err))
	_, err = f.app.AcquireMounts(t.Context(), "work-1", []application.MountRequest{{Directory: shown, ReadOnly: true}})
	require.NoError(t, err, "a mapped Directory still mounts read-only")

	// Over the contract, each refusal carries its reason.
	_, err = f.rpc.MapDirectory(f.as("k"), &storageprivatev1.MapDirectoryRequest{Directory: locatedProto(written), Path: "view/p1/case-3/outputs"})
	require.Equal(t, storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_GRANTED_READ_WRITE, storageerror.ReasonOf(err))
}

// Overlapping paths, NAS Directories and reclaimed Directories are refused,
// each with its contract reason.
func TestMappingsRefuseOverlapsTheNASAndReclaimedDirectories(t *testing.T) {
	f := newFixture(t)
	mapping := func(located application.Located, path string) storageprivatev1.ErrorReason {
		_, err := f.rpc.MapDirectory(f.as("k"), &storageprivatev1.MapDirectoryRequest{Directory: locatedProto(located), Path: path})
		if err == nil {
			return storageprivatev1.ErrorReason_ERROR_REASON_UNSPECIFIED
		}
		return storageerror.ReasonOf(err)
	}
	inputs := fileSpace(named("inputs", "subject", "case:4"))
	artifacts := fileSpace(named("artifacts", "subject", "case:4"))
	require.NoError(t, f.mapAt(inputs, "view/p1/case-4"))
	for _, path := range []string{"view/p1/case-4", "view/p1/case-4/inputs", "view/p1"} {
		require.Equal(t, storageprivatev1.ErrorReason_ERROR_REASON_MAPPING_OVERLAPS, mapping(artifacts, path), path)
	}
	require.NoError(t, f.mapAt(artifacts, "view/p1/case-40"), "a sibling path is apart")

	nas := application.Located{Directory: named("notes", "subject", "case:4"), Backend: domain.BackendNAS, Lifetime: domain.LifetimePermanent}
	require.Equal(t, storageprivatev1.ErrorReason_ERROR_REASON_BACKEND_NOT_MAPPABLE, mapping(nas, "view/p2/notes"))
	require.NoDirExists(t, f.folder(nas), "a refused mapping opens nothing")

	outputs := application.Located{Directory: named("outputs", "work", "work-9"), Backend: domain.BackendJuiceFS, Lifetime: domain.LifetimeWork}
	f.open(outputs)
	require.NoError(t, f.app.RequestWorkReclamation(t.Context(), "work-9"))
	require.Equal(t, storageprivatev1.ErrorReason_ERROR_REASON_DIRECTORY_RECLAIMED, mapping(outputs, "view/p3/outputs"))
	require.Equal(t, storageprivatev1.ErrorReason_ERROR_REASON_INVALID_REQUEST, mapping(inputs, "view/../escape"))
}

// Directories mapped at the same time at nested paths never both hold
// their mapping: one of them is refused as overlapping.
func TestConcurrentMappingsAtNestedPathsNeverOverlap(t *testing.T) {
	f := newFixture(t)
	for round := range 5 {
		outer := fileSpace(named("inputs", "subject", fmt.Sprintf("outer:%d", round)))
		inner := fileSpace(named("inputs", "subject", fmt.Sprintf("inner:%d", round)))
		f.open(outer)
		f.open(inner)
		results := make(chan error, 2)
		go func() { results <- f.mapAt(outer, fmt.Sprintf("view/race-%d", round)) }()
		go func() { results <- f.mapAt(inner, fmt.Sprintf("view/race-%d/inner", round)) }()
		first, second := <-results, <-results
		require.True(t, first == nil != (second == nil), "exactly one maps: %v, %v", first, second)
		for _, err := range []error{first, second} {
			if err != nil {
				require.ErrorIs(t, err, domain.ErrMappingOverlaps)
			}
		}
	}
}

// The Connect handler admits a mapping only beneath the prefix configured
// for the caller's token.
func TestTheCallersPrefixBoundsItsMappings(t *testing.T) {
	f := newFixture(t)
	inputs := fileSpace(named("inputs", "subject", "case:5"))
	_, err := f.rpc.MapDirectory(f.as("k"), &storageprivatev1.MapDirectoryRequest{Directory: locatedProto(inputs), Path: "view/p1/case-5/inputs"})
	require.NoError(t, err)
	require.DirExists(t, f.viewOf("view/p1/case-5/inputs"))

	for name, call := range map[string]struct {
		token, path string
	}{
		"outside its prefix":        {"k", "other/case-5/inputs"},
		"at its prefix itself":      {"k", "view"},
		"a caller without a prefix": {"i", "view/p1/case-5/elsewhere"},
	} {
		_, err := f.rpc.MapDirectory(f.as(call.token), &storageprivatev1.MapDirectoryRequest{Directory: locatedProto(inputs), Path: call.path})
		require.Equal(t, storageprivatev1.ErrorReason_ERROR_REASON_MAPPING_OUTSIDE_PREFIX, storageerror.ReasonOf(err), name)
	}
	_, err = f.rpc.UnmapDirectory(f.as("k"), &storageprivatev1.UnmapDirectoryRequest{Directory: locatedProto(inputs).GetDirectory(), Path: "other/case-5/inputs"})
	require.Equal(t, storageprivatev1.ErrorReason_ERROR_REASON_MAPPING_OUTSIDE_PREFIX, storageerror.ReasonOf(err))
	_, err = f.rpc.MapDirectory(f.as("nobody"), &storageprivatev1.MapDirectoryRequest{Directory: locatedProto(inputs), Path: "view/p9/x"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.NoDirExists(t, f.viewOf("view/p9"))

	_, err = f.rpc.UnmapDirectory(f.as("k"), &storageprivatev1.UnmapDirectoryRequest{Directory: locatedProto(inputs).GetDirectory(), Path: "view/p1/case-5/inputs"})
	require.NoError(t, err)
	require.NoDirExists(t, f.viewOf("view/p1"))
}

// as is a request context carrying a control token.
func (f *fixture) as(token string) context.Context {
	ctx, info := connect.NewClientContext(f.t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	return ctx
}

func locatedProto(located application.Located) *storageprivatev1.LocatedDirectory {
	directory := &storageprivatev1.Directory{Owner: &storageprivatev1.DirectoryOwner{}, Name: located.Directory.Name}
	for _, fact := range located.Directory.Owner {
		directory.Owner.Facts = append(directory.Owner.Facts, &storageprivatev1.OwnerFact{Condition: fact.Condition, Value: fact.Value})
	}
	backend := storageprivatev1.Backend_BACKEND_JUICEFS
	if located.Backend == domain.BackendNAS {
		backend = storageprivatev1.Backend_BACKEND_NAS
	}
	lifetime := storageprivatev1.Lifetime_LIFETIME_PERMANENT
	if located.Lifetime == domain.LifetimeWork {
		lifetime = storageprivatev1.Lifetime_LIFETIME_WORK
	}
	return &storageprivatev1.LocatedDirectory{Directory: directory, Backend: backend, Lifetime: lifetime}
}
