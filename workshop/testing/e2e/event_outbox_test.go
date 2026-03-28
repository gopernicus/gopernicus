//go:build e2e

// This file is created once by gopernicus and will NOT be overwritten.
// Add custom E2E tests for EventOutbox here.
//
// Use setupTestServer() from setup_test.go and testhttp.Client for requests:
//
//	func TestCustomEventOutbox_BusinessLogic(t *testing.T) {
//		ctx, db, ts := setupTestServer(t)
//		client := testhttp.New(ts.URL())
//		// client.SetBearerToken(token)
//
//		created := fixtures.CreateTestEventOutboxWithDefaults(t, ctx, db)
//		resp := client.Get(t, "/your/path/" + created.EventID)
//		resp.RequireStatus(t, 200)
//	}

package e2e
