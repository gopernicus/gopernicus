package session

import "time"

// AuthenticationMetadata records how and when the session's holder last performed
// a primary authentication (design §5.0). It backs the recent-primary-login
// shortcut: a sufficiently recent, sufficiently strong login can satisfy a
// recent-authentication grant (see domain/authgrant) without prompting the user
// for an extra step-up. Zero value means no primary authentication is recorded on
// the session — the shortcut never fires and step-up is always required.
//
// The persisted session columns backing these fields land with the phase-2
// schema; this type freezes the vocabulary the shortcut reads.
type AuthenticationMetadata struct {
	// AuthenticatedAt is when the recorded primary authentication happened.
	AuthenticatedAt time.Time
	// Methods are the descriptors the login was performed with.
	Methods []AuthenticationMethod
	// Assurance is the assurance level the login achieved.
	Assurance AssuranceLevel
}

// Recorded reports whether a primary authentication is recorded on the metadata.
func (m AuthenticationMetadata) Recorded() bool {
	return !m.AuthenticatedAt.IsZero()
}
