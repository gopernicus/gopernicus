package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
)

// All reads precede every write, as Firestore transactions require.
type credentialRevocations struct {
	sessionRows []sessionDoc
	grants      []authGrantDoc
	resets      []challengeDoc
}

func readCredentialRevocations(ctx context.Context, db *firestoredb.DB, userID string) (credentialRevocations, error) {
	r := db.ReaderFrom(ctx)
	var out credentialRevocations
	var err error
	if out.sessionRows, err = readSessionsForUser(ctx, db, r, userID); err != nil {
		return out, err
	}
	if out.grants, err = readGrantsForUser(ctx, db, r, userID); err != nil {
		return out, err
	}
	out.resets, err = readPasswordResetChallenges(ctx, db, r, userID)
	return out, err
}
func (v credentialRevocations) apply(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan) error {
	if err := dropSessionsForUser(ctx, db, w, plan, v.sessionRows); err != nil {
		return err
	}
	if err := dropAuthGrants(ctx, db, w, v.grants); err != nil {
		return err
	}
	return dropPasswordResetChallenges(ctx, db, w, plan, v.resets)
}
