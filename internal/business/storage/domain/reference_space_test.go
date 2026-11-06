package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

func TestReferencePathsStayInTheirContent(t *testing.T) {
	// A hidden name is the writer's own; a mounted space shows only visible entries.
	for _, valid := range []string{"index.md", "pages/payments.md", "pages/品牌口号.md", ".handbook.json"} {
		_, err := domain.ParseReferencePath(valid)
		require.NoError(t, err, valid)
	}
	for _, invalid := range []string{"", "/index.md", "../x.md", "pages/../x.md", "pages//x.md", ".", "..", "a\\b"} {
		_, err := domain.ParseReferencePath(invalid)
		require.ErrorIs(t, err, domain.ErrInvalidReferencePath, invalid)
	}
}

// A Reference Space mounts at one visible directory of the workspace root,
// never inputs/, each name once.
func TestReferenceMountNames(t *testing.T) {
	mounts, err := domain.NewReferenceMounts([]domain.ReferenceMount{{Name: "handbook", Owner: "p-1"}, {Name: "guides", Owner: "p-2"}})
	require.NoError(t, err)
	require.Len(t, mounts, 2)
	for _, name := range []string{"", "inputs", ".hidden", "a/b", " handbook", ".."} {
		_, err := domain.NewReferenceMount(name, "p-1")
		require.ErrorIs(t, err, domain.ErrInvalidReferenceMount, name)
	}
	_, err = domain.NewReferenceMounts([]domain.ReferenceMount{{Name: "handbook", Owner: "p-1"}, {Name: "handbook", Owner: "p-2"}})
	require.ErrorIs(t, err, domain.ErrInvalidReferenceMount)
	_, err = domain.NewReferenceMount("handbook", "a/b")
	require.ErrorIs(t, err, domain.ErrInvalidOwner)
}
