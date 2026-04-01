---
name: new-case
description: Interactive workflow to design and build a use case for business logic beyond CRUD
---

# New Case Workflow

You are guiding the user through creating a use case — business logic that goes beyond simple CRUD. Move through each phase one at a time, asking questions before proceeding.

## Phase 1: Understand the Use Case

Start by asking:

- What does this use case do? Describe the business operation in plain language.
- What is a good name for it? (e.g., "projectadmin", "billing", "onboarding") — this becomes the package name, so it should be short and lowercase.
- What existing repositories or services does it need to interact with?
- Does it need to emit domain events? (e.g., "user_onboarded", "invoice_created")
- Does it need to coordinate multiple steps that should succeed or fail together?

Confirm back: "So [case name] will [description], using [repos/services], and [emitting/not emitting] events — correct?"

## Phase 2: Scaffold

1. Run: `gopernicus new case <name>`
2. Show the user the six files that were created:
   - core/cases/<name>/case.go — Case struct, constructor, interfaces
   - core/cases/<name>/errors.go — domain errors
   - core/cases/<name>/events.go — domain events
   - bridge/cases/<name>bridge/bridge.go — Bridge struct
   - bridge/cases/<name>bridge/http.go — route registration
   - bridge/cases/<name>bridge/model.go — request/response types

## Phase 3: Define Dependencies

Work through the dependency interfaces in case.go:

- For each repository or service the case needs, ask:
  - What methods does this case actually call on it? (Keep interfaces narrow — only what this case uses)
  - What are the input/output types?
- Write the interface definitions at the top of case.go
- Add the dependencies to the Case struct and constructor

Show the code and confirm before moving on.

## Phase 4: Design Operations

For each operation the case performs:

- Ask: What are the inputs? What does it return?
- Ask: What are the business rules? (e.g., "can only archive if not already archived", "must notify members first")
- Ask: What errors can occur? (not found, conflict, forbidden, validation)
- Ask: Should this emit an event?
- Write the operation method, input/result types, and error definitions
- Show the code and confirm

Do one operation at a time. Ask "Are there more operations for this case?" before moving on.

## Phase 5: Define Errors

Based on the operations designed, write error definitions in errors.go:

- Wrap sentinel errors from sdk/errs so the bridge maps them to correct HTTP status codes
- Common mappings: errs.ErrNotFound (404), errs.ErrConflict (409), errs.ErrForbidden (403), errs.ErrBadRequest (400)
- Show the errors and confirm

## Phase 6: Define Events (if applicable)

If the case emits events:

- For each event type, ask about the payload fields
- Events embed events.BaseEvent and use the naming convention "<case>.<action>" (e.g., "billing.invoice_created")
- Write the event types in events.go
- Show and confirm

## Phase 7: Build the Bridge

For each operation, work through the HTTP layer:

- Ask: What HTTP method and path? Cases mount under /cases/<kebab-name>/
- Ask: What middleware? (authenticate, rate_limit, authorize, max_body_size)
- Write the request/response types in model.go with Validate() methods
- Write the handler in http.go
- Register the route in AddHttpRoutes

Show the route table and confirm.

## Phase 8: Wire in Server

1. Read app/server/config/server.go to understand existing wiring
2. Show what needs to be added:
   - Construct the case with its dependencies
   - Construct the bridge wrapping the case
   - Mount routes: cases := api.Group("/cases"); <name>Bridge.AddHttpRoutes(cases)
3. Help write the wiring code

## Phase 9: Verify

1. Run: `go build ./...` — confirm everything compiles
2. Summarize: what was created, what routes are available, and any next steps
3. Ask: "Would you like to add event subscribers for the events this case emits?"

## Reference

See workshop/documentation/gopernicus/guides/adding-use-case.md for the full guide.
