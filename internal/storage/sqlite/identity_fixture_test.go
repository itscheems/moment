package sqlite

import (
	"context"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

const testAccountID = "0123456789abcdef0123456789abcdef"

func testAccountContext() context.Context {
	return app.WithContentActor(context.Background(), domain.User{ID: testAccountID, Username: "writer", DisplayName: "Writer", Role: domain.UserRoleAdmin, Status: domain.AccountActive})
}
