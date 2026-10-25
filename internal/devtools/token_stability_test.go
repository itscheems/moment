package devtools

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestSavedDevelopmentTokenAuthenticatesAfterRebuild(t *testing.T) {
	root := developmentRuntime(t).Root
	fixed := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	for _, seed := range []int64{1, 2} {
		result, err := Execute(context.Background(), Request{DataDir: root, Operation: OperationScenario, Scenario: "timeline-basic", Seed: seed, APIToken: fixed})
		if err != nil || result.APIToken != fixed {
			t.Fatalf("saved credential was not reused: %v", err)
		}
		state, err := runtimefs.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		db, err := sqlitestore.Open(context.Background(), state, sqlitestore.Options{})
		if err != nil {
			t.Fatal(err)
		}
		repository, err := sqlitestore.NewAccountRepository(db, state.Locks)
		if err != nil {
			t.Fatal(err)
		}
		accounts, err := app.NewAccountService(repository)
		if err != nil {
			t.Fatal(err)
		}
		profile, err := accounts.ProfileAccount(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		verified, err := accounts.ResolveAPIToken(context.Background(), fixed)
		if err != nil || verified.User.ID != profile.ID {
			t.Fatalf("credential does not resolve the real profile account: %v", err)
		}
		other := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
		if _, err := accounts.ResolveAPIToken(context.Background(), other); !domain.HasCode(err, domain.CodeUnauthorized) {
			t.Fatalf("wrong token accepted: %v", err)
		}
		issued, err := accounts.CreateAPIToken(context.Background(), profile.ID, "ordinary credential")
		if err != nil || issued.Value == fixed {
			t.Fatalf("fixture entropy affected ordinary issuance: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Execute(context.Background(), Request{DataDir: root, Operation: OperationScenario, Scenario: "timeline-basic", Seed: 3})
	if err != nil || result.APIToken == fixed {
		t.Fatalf("default fixture issuance stopped rotating: %v", err)
	}
}

func TestInvalidSavedDevelopmentTokenDoesNotReplaceData(t *testing.T) {
	state := developmentRuntime(t)
	before := state.Current.ID
	_, err := Execute(context.Background(), Request{DataDir: state.Root, Operation: OperationScenario, Scenario: "timeline-basic", APIToken: "invalid"})
	if !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("invalid token: %v", err)
	}
	current, err := state.ResolveCurrent()
	if err != nil || current.ID != before {
		t.Fatalf("invalid input changed the fixture: %v", err)
	}
}
