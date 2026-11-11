package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func prefix(t *testing.T, raw string) MappingPrefix {
	t.Helper()
	p, err := NewMappingPrefix(raw)
	require.NoError(t, err)
	return p
}

func mapped(t *testing.T, raw string) MappedPath {
	t.Helper()
	p, err := ParseMappedPath(raw)
	require.NoError(t, err)
	return p
}

// allocatedSpace is an Allocated permanent Directory on the backend.
func allocatedSpace(t *testing.T, backend Backend) *Directory {
	t.Helper()
	d, err := NewDirectory(located(t, owner(t, "subject", "case:1"), "inputs", backend, LifetimePermanent))
	require.NoError(t, err)
	require.NoError(t, d.RecordAllocation())
	return d
}

func TestAMappedPathIsACleanRelativePathOfVisibleElements(t *testing.T) {
	require.Equal(t, MappedPath("view/p1/i1/inputs"), mapped(t, "view/p1/i1/inputs"))
	for _, raw := range []string{"", ".", "..", "/view", "view/", "view//a", "view/../a", "view/.hidden", ".view", "a\\b", "a\x00b",
		"view/a ", "view/ a", "view/\xff", strings.Repeat("a", MaxMappedPathCharacters+1)} {
		_, err := ParseMappedPath(raw)
		require.ErrorIs(t, err, ErrInvalidMappedPath, raw)
	}
	require.True(t, mapped(t, "view/a").Overlaps("view/a"))
	require.True(t, mapped(t, "view/a").Overlaps("view/a/b"), "a path above another overlaps it")
	require.True(t, mapped(t, "view/a/b").Overlaps("view/a"), "a path inside another overlaps it")
	require.False(t, mapped(t, "view/a").Overlaps("view/ab"), "a sibling sharing a prefix of its name does not")
}

func TestACallerMapsOnlyStrictlyBeneathItsPrefix(t *testing.T) {
	view := prefix(t, "view")
	require.NoError(t, view.Admit("view/a"))
	require.ErrorIs(t, view.Admit("view"), ErrMappingOutsidePrefix, "the prefix itself is shared ground")
	require.ErrorIs(t, view.Admit("viewer/a"), ErrMappingOutsidePrefix)
	require.ErrorIs(t, view.Admit("other/a"), ErrMappingOutsidePrefix)
	require.ErrorIs(t, MappingPrefix{}.Admit("view/a"), ErrMappingOutsidePrefix, "a caller without a prefix maps nowhere")
	_, err := NewMappingPrefix("../view")
	require.ErrorIs(t, err, ErrInvalidMappedPath)
}

func TestADirectoryIsMappedOnlyOnJuiceFSBeneathItsCallersPrefixAndApartFromOthers(t *testing.T) {
	d := allocatedSpace(t, BackendJuiceFS)
	view := prefix(t, "view")
	require.NoError(t, d.Map("view/a", view, true))
	require.NoError(t, d.Map("view/a", view, false), "mapping it again at its own path changes nothing")
	require.Equal(t, []MappedPath{"view/a"}, d.Mappings)
	require.ErrorIs(t, d.Map("view/b", view, false), ErrMappingOverlaps)
	require.NoError(t, d.Map("view/b", view, true), "one Directory may be mapped at several paths")
	require.ErrorIs(t, d.Map("other/c", view, true), ErrMappingOutsidePrefix)
	require.True(t, d.MappedAt("view/b"))

	require.NoError(t, d.Unmap("view/a", view))
	require.NoError(t, d.Unmap("view/a", view), "removing an absent mapping changes nothing")
	require.Equal(t, []MappedPath{"view/b"}, d.Mappings)
	require.ErrorIs(t, d.Unmap("other/b", view), ErrMappingOutsidePrefix)

	require.ErrorIs(t, allocatedSpace(t, BackendNAS).Map("view/c", view, true), ErrBackendNotMappable)
	allocating, err := NewDirectory(located(t, owner(t, "subject", "case:2"), "inputs", BackendJuiceFS, LifetimePermanent))
	require.NoError(t, err)
	require.ErrorIs(t, allocating.Map("view/d", view, true), ErrDirectoryNotAllocated)
}

func TestReadWriteGrantsAndMappingsExcludeEachOther(t *testing.T) {
	view := prefix(t, "view")
	written := allocatedSpace(t, BackendJuiceFS)
	_, err := written.GrantMount(true, nil)
	require.NoError(t, err)
	require.False(t, written.GrantedReadWrite, "a read-only grant records nothing")
	_, err = written.GrantMount(false, nil)
	require.NoError(t, err)
	require.True(t, written.GrantedReadWrite)
	require.ErrorIs(t, written.Map("view/a", view, true), ErrGrantedReadWrite, "a Directory once granted read-write is never mapped")

	shown := allocatedSpace(t, BackendJuiceFS)
	require.NoError(t, shown.Map("view/b", view, true))
	_, err = shown.GrantMount(false, nil)
	require.ErrorIs(t, err, ErrDirectoryMapped)
	_, err = shown.GrantMount(true, nil)
	require.NoError(t, err, "a mapped Directory still mounts read-only")
	require.False(t, shown.GrantedReadWrite)
}

func TestReclamationEndsEveryMapping(t *testing.T) {
	d, err := NewDirectory(located(t, owner(t, "work", "w1"), "outputs", BackendJuiceFS, LifetimeWork))
	require.NoError(t, err)
	require.NoError(t, d.RecordAllocation())
	require.NoError(t, d.Map("view/a", prefix(t, "view"), true))
	require.NoError(t, d.Reclaim())
	require.Empty(t, d.Mappings)
	require.ErrorIs(t, d.Map("view/a", prefix(t, "view"), true), ErrDirectoryReclaimed)
}

func TestOnlyAJuiceFSDirectoryIsAdmittedForMapping(t *testing.T) {
	require.NoError(t, located(t, owner(t, "subject", "case:1"), "inputs", BackendJuiceFS, LifetimePermanent).AdmitMapping())
	require.ErrorIs(t, located(t, owner(t, "subject", "case:1"), "notes", BackendNAS, LifetimePermanent).AdmitMapping(), ErrBackendNotMappable)
}
