package satisfiers

import (
	"context"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/repositories/auth/sessions"
)

var _ authentication.SessionRepository = (*SessionSatisfier)(nil)

type sessionRepo interface {
	Create(ctx context.Context, input sessions.CreateSession) (sessions.Session, error)
	Update(ctx context.Context, sessionID string, parentUserID string, input sessions.UpdateSession) (sessions.Session, error)
	GetByTokenHash(ctx context.Context, sessionTokenHash string) (sessions.Session, error)
	GetByRefreshHash(ctx context.Context, refreshTokenHash string) (sessions.Session, error)
	GetByPreviousRefreshHash(ctx context.Context, previousRefreshTokenHash string) (sessions.Session, error)
	Delete(ctx context.Context, sessionID string, parentUserID string) error
	DeleteAllForUser(ctx context.Context, parentUserID string) error
	DeleteAllForUserExcept(ctx context.Context, parentUserID string, sessionID string) error
}

// SessionSatisfier satisfies authentication.SessionRepository using the generated sessions repository.
type SessionSatisfier struct {
	repo sessionRepo
}

func NewSessionSatisfier(repo sessionRepo) *SessionSatisfier {
	return &SessionSatisfier{repo: repo}
}

func (s *SessionSatisfier) Create(ctx context.Context, sess authentication.Session) (authentication.Session, error) {
	created, err := s.repo.Create(ctx, sessions.CreateSession{
		SessionID:                sess.SessionID,
		ParentUserID:             sess.UserID,
		SessionTokenHash:         sess.TokenHash,
		RefreshTokenHash:         &sess.RefreshTokenHash,
		PreviousRefreshTokenHash: &sess.PreviousRefreshHash,
		RotationCount:            &sess.RotationCount,
		ExpiresAt:                sess.ExpiresAt,
	})
	if err != nil {
		return authentication.Session{}, err
	}
	return toAuthSession(created), nil
}

func (s *SessionSatisfier) GetByTokenHash(ctx context.Context, hash string) (authentication.Session, error) {
	sess, err := s.repo.GetByTokenHash(ctx, hash)
	if err != nil {
		return authentication.Session{}, err
	}
	return toAuthSession(sess), nil
}

func (s *SessionSatisfier) GetByRefreshHash(ctx context.Context, hash string) (authentication.Session, error) {
	sess, err := s.repo.GetByRefreshHash(ctx, hash)
	if err != nil {
		return authentication.Session{}, err
	}
	return toAuthSession(sess), nil
}

func (s *SessionSatisfier) GetByPreviousRefreshHash(ctx context.Context, hash string) (authentication.Session, error) {
	sess, err := s.repo.GetByPreviousRefreshHash(ctx, hash)
	if err != nil {
		return authentication.Session{}, err
	}
	return toAuthSession(sess), nil
}

func (s *SessionSatisfier) Update(ctx context.Context, sess authentication.Session) error {
	_, err := s.repo.Update(ctx, sess.SessionID, sess.UserID, sessions.UpdateSession{
		SessionTokenHash:         &sess.TokenHash,
		RefreshTokenHash:         &sess.RefreshTokenHash,
		PreviousRefreshTokenHash: &sess.PreviousRefreshHash,
		RotationCount:            &sess.RotationCount,
		ExpiresAt:                &sess.ExpiresAt,
	})
	return err
}

func (s *SessionSatisfier) Delete(ctx context.Context, userID, sessionID string) error {
	return s.repo.Delete(ctx, sessionID, userID)
}

func (s *SessionSatisfier) DeleteAllForUser(ctx context.Context, userID string) error {
	return s.repo.DeleteAllForUser(ctx, userID) // userID maps to parent_user_id
}

func (s *SessionSatisfier) DeleteAllForUserExcept(ctx context.Context, userID, exceptSessionID string) error {
	return s.repo.DeleteAllForUserExcept(ctx, userID, exceptSessionID) // userID maps to parent_user_id
}

func toAuthSession(sess sessions.Session) authentication.Session {
	as := authentication.Session{
		SessionID: sess.SessionID,
		UserID:    sess.ParentUserID,
		TokenHash: sess.SessionTokenHash,
		ExpiresAt: sess.ExpiresAt,
	}
	if sess.RefreshTokenHash != nil {
		as.RefreshTokenHash = *sess.RefreshTokenHash
	}
	if sess.PreviousRefreshTokenHash != nil {
		as.PreviousRefreshHash = *sess.PreviousRefreshTokenHash
	}
	if sess.RotationCount != nil {
		as.RotationCount = *sess.RotationCount
	}
	return as
}
