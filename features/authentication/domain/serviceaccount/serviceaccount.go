// Package serviceaccount is the machine-identity domain: a non-human principal
// that authenticates exclusively via API keys (logic/apikey). A service account
// either acts as itself (Principal{Type: "service_account"}) or, when ActAsUser
// is set, acts on behalf of a human owner (a personal key → Principal{Type:
// "user", ID: OwnerUserID}); the effective-principal resolution lives in the
// auth service (design §4.1). There is no principals registry table (AV5) —
// actor references are (subject_type, subject_id) string pairs everywhere.
package serviceaccount

import (
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
)

// ServiceAccount is a machine identity. CreatedBy is the human user that minted
// it. ActAsUser marks a personal service account whose keys resolve to the human
// OwnerUserID rather than to the account itself; the construction invariant
// ActAsUser → OwnerUserID != "" is enforced in New.
type ServiceAccount struct {
	ID          string
	Name        string
	Description string
	CreatedBy   string
	ActAsUser   bool
	OwnerUserID string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// New builds a ServiceAccount created by createdBy as of now, minting its ID
// from ids (empty under cryptids.Database — the store then assigns the key).
// A blank name, a blank createdBy, or an act-as-user account with no OwnerUserID
// wraps sdk.ErrInvalidInput (the design §4.1 invariant: ActAsUser →
// OwnerUserID != "").
func New(ids cryptids.IDGenerator, name, description, createdBy string, actAsUser bool, ownerUserID string, now time.Time) (ServiceAccount, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	createdBy = strings.TrimSpace(createdBy)
	ownerUserID = strings.TrimSpace(ownerUserID)
	if name == "" {
		return ServiceAccount{}, fmt.Errorf("name is required: %w", sdk.ErrInvalidInput)
	}
	if createdBy == "" {
		return ServiceAccount{}, fmt.Errorf("created-by is required: %w", sdk.ErrInvalidInput)
	}
	if actAsUser && ownerUserID == "" {
		return ServiceAccount{}, fmt.Errorf("act-as-user requires an owner user id: %w", sdk.ErrInvalidInput)
	}
	now = now.UTC()
	return ServiceAccount{
		ID:          ids.MustGenerate(),
		Name:        name,
		Description: description,
		CreatedBy:   createdBy,
		ActAsUser:   actAsUser,
		OwnerUserID: ownerUserID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}
