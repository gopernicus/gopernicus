// Package notify is the facility port for delivering a message to one address of
// a declared kind. It sits next to sdk/email: email remains the mail facility,
// and this package's MailerBridge adapts an email.Sender so the email kind can
// route through the same delivery seam as every other kind — bridging, not
// replacing.
//
// Deny-by-absence: a host wires one Notifier per address kind it supports, and
// the wired set DEFINES the host's supported kinds. An unwired kind is
// structurally OFF — there is no default fan-out and no fallback delivery. This
// mirrors the oauth Providers precedent: capability is presence, not
// configuration.
//
// Fail loudly: a Notifier delivers or returns an error. It never silently drops
// a message. Console logs every delivery; a real provider propagates transport
// failures to its caller.
//
// Message is deliberately minimal in v1 — subject and body only. Templating and
// rich content are the consumer's job pre-render; a richer payload vocabulary is
// future work, not now.
//
// Kind values are the sdk/identity Address-kind vocabulary (identity.KindEmail,
// identity.KindPhone, or any open string a provider declares). The intra-sdk
// import of sdk/identity for Address is a self-module import (G1-legal).
package notify

import (
	"context"

	"github.com/gopernicus/gopernicus/sdk/identity"
)

// Message is a message to deliver. It is deliberately minimal in v1: Subject and
// Body only. Consumers pre-render templates and rich content into Body; a richer
// payload is future vocabulary.
type Message struct {
	Subject string
	Body    string
}

// Notifier delivers a Message to one address of the kind it declares.
//
// Kind() is the deny-by-absence hook: a host wires one Notifier per address kind
// it supports, and the wired set defines the host's supported kinds. An unwired
// kind is structurally off.
//
// Notify fails loudly: it delivers or returns an error, and never silently drops
// a message. Kind values are the identity Address-kind vocabulary
// (identity.KindEmail, identity.KindPhone, or an open string).
type Notifier interface {
	Kind() string
	Notify(ctx context.Context, to identity.Address, msg Message) error
}
