package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func owner(t *testing.T, facts ...string) DirectoryOwner {
	t.Helper()
	var parsed []OwnerFact
	for i := 0; i < len(facts); i += 2 {
		parsed = append(parsed, OwnerFact{Condition: facts[i], Value: facts[i+1]})
	}
	o, err := NewDirectoryOwner(parsed)
	require.NoError(t, err)
	return o
}

func located(t *testing.T, o DirectoryOwner, name string, backend Backend, lifetime Lifetime) LocatedDirectory {
	t.Helper()
	id, err := NewDirectoryID(o, name)
	require.NoError(t, err)
	l, err := NewLocatedDirectory(id, backend, lifetime)
	require.NoError(t, err)
	return l
}

func workDirectory(t *testing.T) *Directory {
	t.Helper()
	d, err := NewDirectory(located(t, owner(t, "work", "w1"), "home", BackendNAS, LifetimeWork))
	require.NoError(t, err)
	return d
}

func TestALocatorAppliedToFactsLocatesOneOwnerAndName(t *testing.T) {
	o := owner(t, "project", "p1", "subject", "case:1")
	require.Equal(t, []OwnerFact{{"project", "p1"}, {"subject", "case:1"}}, o.Facts())
	require.Equal(t, owner(t, "project", "p1", "subject", "case:1"), o, "the same facts locate the same owner")
	require.NotEqual(t, owner(t, "subject", "case:1"), o)
	_, names := o.Work()
	require.False(t, names, "Storage keeps every other owner opaque")

	work, names := owner(t, "agent", "a1", "work", "w1").Work()
	require.True(t, names)
	require.Equal(t, "w1", work)

	for name, facts := range map[string][]OwnerFact{
		"no fact":           nil,
		"out of order":      {{"subject", "s"}, {"project", "p"}},
		"repeated":          {{"subject", "s"}, {"subject", "t"}},
		"empty value":       {{"subject", ""}},
		"empty condition":   {{"", "s"}},
		"control character": {{"subject", "a\nb"}},
	} {
		_, err := NewDirectoryOwner(facts)
		require.ErrorIs(t, err, ErrInvalidLocator, name)
	}
	for _, name := range []string{"", ".hidden", "a/b", `a\b`, "a\x00b", ".."} {
		_, err := NewDirectoryID(o, name)
		require.ErrorIs(t, err, ErrInvalidLocator, name)
	}
}

func TestAWorkLifetimeNeedsAnOwnerNamingTheWork(t *testing.T) {
	id, err := NewDirectoryID(owner(t, "subject", "case:1"), "inputs")
	require.NoError(t, err)
	_, err = NewLocatedDirectory(id, BackendJuiceFS, LifetimeWork)
	require.ErrorIs(t, err, ErrInvalidLocator)
	_, err = NewLocatedDirectory(id, BackendJuiceFS, LifetimePermanent)
	require.NoError(t, err)
	_, err = NewLocatedDirectory(id, "tape", LifetimePermanent)
	require.ErrorIs(t, err, ErrInvalidLocator)
	_, err = NewLocatedDirectory(id, BackendNAS, "")
	require.ErrorIs(t, err, ErrInvalidLocator)
}

func TestADirectoryIsMountedAndWrittenOnlyOnceAllocated(t *testing.T) {
	d := workDirectory(t)
	require.Equal(t, DirectoryAllocating, d.State)
	require.NotEmpty(t, d.SpaceToken)
	require.True(t, d.AwaitsAllocation())
	writer, _ := AdmittedWriter(Actor{UserID: "u1", ApplicationID: ""})

	_, err := d.GrantMount(false, nil)
	require.ErrorIs(t, err, ErrDirectoryNotAllocated, "nothing is mounted before its space exists")
	require.ErrorIs(t, d.AdmitFileWrite(d.SpaceToken, writer), ErrDirectoryNotAllocated)
	require.ErrorIs(t, d.AdmitContentReplacement(d.SpaceToken), ErrDirectoryNotAllocated)

	require.NoError(t, d.RecordAllocation())
	require.Equal(t, DirectoryAllocated, d.State)
	require.False(t, d.AwaitsAllocation())
	require.NoError(t, d.RecordAllocation(), "recording it again changes nothing")
	require.Equal(t, DirectoryAllocated, d.State)

	grant, err := d.GrantMount(true, []string{".claude", "a/b", ".claude"})
	require.NoError(t, err)
	require.Equal(t, MountAdmission{Directory: d.ID, ReadOnly: true, MountPoints: []MountPoint{".claude", "a/b"}}, grant)
	for _, point := range []string{"", "/abs", "../up", "a/../b", "."} {
		_, err := d.GrantMount(false, []string{point})
		require.ErrorIs(t, err, ErrInvalidMountPoint, point)
	}
	require.NoError(t, d.AdmitFileWrite(d.SpaceToken, writer))
	require.NoError(t, d.AdmitContentReplacement(d.SpaceToken))
}

func TestAWriteNeedsTheSpaceTokenAndItsWriter(t *testing.T) {
	d := workDirectory(t)
	require.NoError(t, d.RecordAllocation())
	writer, _ := WorkWriter("w1")
	require.ErrorIs(t, d.AdmitFileWrite("", writer), ErrSpaceTokenRejected)
	require.ErrorIs(t, d.AdmitFileWrite("guess", writer), ErrSpaceTokenRejected)
	require.ErrorIs(t, d.AdmitFileWrite(d.SpaceToken, FileWriter{}), ErrFileWriterRequired)
	require.ErrorIs(t, d.AdmitContentReplacement("guess"), ErrSpaceTokenRejected)
}

func TestOnlyAWorkDirectoryIsReclaimedAndReclamationIsFinal(t *testing.T) {
	permanent, err := NewDirectory(located(t, owner(t, "agent", "a1"), "claude", BackendNAS, LifetimePermanent))
	require.NoError(t, err)
	require.ErrorIs(t, permanent.Reclaim(), ErrPermanentDirectory)
	require.Equal(t, DirectoryAllocating, permanent.State)

	allocating := workDirectory(t)
	require.NoError(t, allocating.Reclaim(), "an unfinished allocation is reclaimed too")
	require.Equal(t, DirectoryReclaimed, allocating.State)

	d := workDirectory(t)
	require.NoError(t, d.RecordAllocation())
	require.NoError(t, d.Reclaim())
	require.Equal(t, DirectoryReclaimed, d.State)
	require.NoError(t, d.Reclaim(), "a repeated request changes nothing")
	require.Equal(t, DirectoryReclaimed, d.State)

	writer, _ := AdmittedWriter(Actor{UserID: "u1", ApplicationID: ""})
	require.ErrorIs(t, d.Open(), ErrDirectoryReclaimed, "a reclaimed Directory is never recreated")
	require.ErrorIs(t, d.RecordAllocation(), ErrDirectoryReclaimed)
	_, err = d.GrantMount(false, nil)
	require.ErrorIs(t, err, ErrDirectoryReclaimed, "a reclaimed Directory is never mounted")
	require.ErrorIs(t, d.AdmitFileWrite(d.SpaceToken, writer), ErrDirectoryReclaimed)
	require.ErrorIs(t, d.AdmitRead(), ErrDirectoryReclaimed, "a read answers that it was reclaimed")
	require.NoError(t, workDirectory(t).AdmitRead())
	require.NoError(t, workDirectory(t).Open())
}

func TestShareLinksFollowRenameAndEndWithDeletion(t *testing.T) {
	d := workDirectory(t)
	now := time.Now()
	report, _ := NewFilePosition(d.ID, "report.md")
	renamed, _ := report.Renamed("final.md")

	link, err := d.CreateShareLink(report, Actor{UserID: "u1"}, now)
	require.NoError(t, err)
	other, err := d.CreateShareLink(report, Actor{UserID: "u2"}, now)
	require.NoError(t, err)
	_, err = d.CreateShareLink(report, Actor{UserID: " "}, now)
	require.ErrorIs(t, err, ErrActorRequired)
	_, err = d.CreateShareLink(report, Actor{UserID: "u1", ApplicationID: "a1"}, now)
	require.ErrorIs(t, err, ErrActorRequired, "a link has one creator")
	byApplication, err := d.CreateShareLink(report, Actor{ApplicationID: " a1 "}, now)
	require.NoError(t, err)
	require.Equal(t, Actor{ApplicationID: "a1"}, byApplication.CreatedBy)
	elsewhere, _ := NewFilePosition(workDirectory(t).ID, "report.md")
	elsewhere.Directory.Name = "other"
	_, err = d.CreateShareLink(elsewhere, Actor{UserID: "u1"}, now)
	require.ErrorIs(t, err, ErrShareLinkNotFound, "a link points into its own Directory")

	d.RecordRename(report, renamed)
	require.Equal(t, renamed, link.Target)
	require.Equal(t, []string{link.Token, other.Token, byApplication.Token}, d.ShareLinkTokens(renamed))
	require.Empty(t, d.ActiveShareLinks(report))

	revoked, remaining := d.RevokeShareLinks(renamed, Actor{UserID: "u2"})
	require.Equal(t, 1, revoked)
	require.Equal(t, 2, remaining)
	revoked, remaining = d.RevokeShareLinks(renamed, Actor{ApplicationID: "a1"})
	require.Equal(t, 1, revoked, "an Application ends the links it created")
	require.Equal(t, 1, remaining)
	require.False(t, other.Active())
	other.Retarget(report)
	require.Equal(t, renamed, other.Target, "a revoked link never moves again")

	d.RecordDeletion(renamed)
	require.False(t, link.Active())
	require.Same(t, link, d.ShareLink(link.Token), "a revoked link stays known")
	require.Nil(t, d.ShareLink("unknown"))
}

func TestStaleShareLinksNeverReachANewFile(t *testing.T) {
	d := workDirectory(t)
	position, _ := NewFilePosition(d.ID, "a.md")
	stale, _ := d.CreateShareLink(position, Actor{UserID: "u1"}, time.Now())
	fresh, _ := d.CreateShareLink(position, Actor{UserID: "u1"}, time.Now())
	d.RevokeStaleShareLinks(position, []string{stale.Token})
	require.False(t, stale.Active())
	require.True(t, fresh.Active(), "a link not found stale stays")
}

func TestFileWriterNamesExactlyOneWriter(t *testing.T) {
	person, err := AdmittedWriter(Actor{UserID: " u1 ", ApplicationID: ""})
	require.NoError(t, err)
	require.Equal(t, "u1", person.UserID())
	require.Empty(t, person.ApplicationID())
	require.Empty(t, person.WorkID())
	application, err := AdmittedWriter(Actor{UserID: "", ApplicationID: "a1"})
	require.NoError(t, err)
	require.Equal(t, "a1", application.ApplicationID())
	require.Empty(t, application.UserID())
	work, err := WorkWriter("w1")
	require.NoError(t, err)
	require.Equal(t, "w1", work.WorkID())
	_, err = AdmittedWriter(Actor{UserID: " ", ApplicationID: ""})
	require.ErrorIs(t, err, ErrFileWriterRequired)
	_, err = AdmittedWriter(Actor{UserID: "u1", ApplicationID: "a1"})
	require.ErrorIs(t, err, ErrFileWriterRequired, "a write is admitted for one person or one Application")
	_, err = WorkWriter("")
	require.ErrorIs(t, err, ErrFileWriterRequired)
}
