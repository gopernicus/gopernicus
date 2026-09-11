package authentication

import (
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
)

// Configuration tests vary one optional feature over these unused core ports.
// Runtime tests supply real repositories; missing-repository tests call NewService
// directly without this fixture.
func testRepositories(repos Repositories) Repositories {
	if repos.Users == nil {
		repos.Users = configUsers{}
	}
	if repos.Identifiers == nil {
		repos.Identifiers = &memIdentifierRepo{}
	}
	if repos.Passwords == nil {
		repos.Passwords = configPasswords{}
	}
	if repos.Sessions == nil {
		repos.Sessions = configSessions{}
	}
	if repos.ActiveSessions == nil {
		repos.ActiveSessions = configActiveSessions{}
	}
	return repos
}

type configUsers struct{ user.UserRepository }
type configPasswords struct{ user.PasswordRepository }
type configSessions struct{ session.SessionRepository }
type configActiveSessions struct{ session.ActiveUserRepository }

type configPasswordResets struct{ passwordreset.Repository }
