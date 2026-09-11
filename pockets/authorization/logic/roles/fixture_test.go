package roles

import "testing"

type roleFixture struct {
	*Service
	*Writer
}

func newRoleFixture(t *testing.T, store Storer) roleFixture {
	t.Helper()
	svc, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewWriter(store)
	if err != nil {
		t.Fatal(err)
	}
	return roleFixture{svc, writer}
}
