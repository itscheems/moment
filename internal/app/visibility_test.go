package app

import (
	"testing"

	"github.com/moment/moment/internal/domain"
)

func TestViewerForGrantsTheWholeSiteOnlyToTheOwner(t *testing.T) {
	owner := domain.User{ID: "owner", Role: domain.UserRoleOwner, Status: domain.AccountActive}
	member := domain.User{ID: "member", Role: domain.UserRoleMember, Status: domain.AccountActive}
	pending := domain.User{ID: "pending", Role: domain.UserRoleMember, Status: domain.AccountPending}
	cases := []struct {
		name     string
		user     domain.User
		signedIn bool
		owner    bool
	}{
		{"nobody", domain.User{}, false, false},
		{"owner", owner, true, true},
		// The bearer token names the owner even when the row cannot be read.
		{"unnamed owner", domain.User{Role: domain.UserRoleOwner, Status: domain.AccountActive}, true, true},
		{"approved member", member, true, false},
		{"pending member", pending, true, false},
		// A credential that was not honoured carries no standing, whatever
		// role the caller claims it had.
		{"owner not signed in", owner, false, false},
	}
	for _, testCase := range cases {
		viewer := ViewerFor(testCase.user, testCase.signedIn)
		if viewer.IsOwner() != testCase.owner {
			t.Fatalf("%s: IsOwner = %v", testCase.name, viewer.IsOwner())
		}
		if !viewer.CanRead(domain.VisibilityEveryone) {
			t.Fatalf("%s: cannot read public posts", testCase.name)
		}
		if viewer.CanRead(domain.VisibilityOnlyMe) != testCase.owner {
			t.Fatalf("%s: CanRead(only_me) = %v", testCase.name, viewer.CanRead(domain.VisibilityOnlyMe))
		}
		if viewer.CanRead("friends") {
			t.Fatalf("%s: reads an unknown visibility", testCase.name)
		}
	}
}

func TestZeroViewerIsAStranger(t *testing.T) {
	var viewer Viewer
	if viewer.IsOwner() || viewer.CanRead(domain.VisibilityOnlyMe) {
		t.Fatal("the zero viewer reads hidden posts")
	}
	if viewer != PublicViewer() {
		t.Fatal("the zero viewer is not the public viewer")
	}
}

// The list a set query filters by is derived from the same decision a single
// read makes, so the two cannot drift apart.
func TestVisibilitiesMirrorCanRead(t *testing.T) {
	for _, viewer := range []Viewer{PublicViewer(), OwnerViewer()} {
		listed := map[domain.Visibility]bool{}
		for _, visibility := range viewer.Visibilities() {
			listed[visibility] = true
		}
		for _, visibility := range domain.Visibilities {
			if listed[visibility] != viewer.CanRead(visibility) {
				t.Fatalf("owner=%v: %q listed=%v readable=%v", viewer.IsOwner(), visibility, listed[visibility], viewer.CanRead(visibility))
			}
		}
	}
	if got := PublicViewer().Visibilities(); len(got) != 1 || got[0] != domain.VisibilityEveryone {
		t.Fatalf("public visibilities = %v", got)
	}
	if got := OwnerViewer().Visibilities(); len(got) != len(domain.Visibilities) {
		t.Fatalf("owner visibilities = %v", got)
	}
}
