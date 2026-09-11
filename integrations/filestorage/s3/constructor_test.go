package s3

import (
	"errors"
	"testing"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gopernicus/gopernicus/sdk"
)

func newTestStore(t *testing.T, client *awss3.Client, bucket string) *Store {
	t.Helper()
	store, err := New(client, bucket)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	client := awss3.New(awss3.Options{Region: "us-east-1"})
	for _, tc := range []struct {
		name   string
		client *awss3.Client
		bucket string
	}{
		{"nil client", nil, "bucket"},
		{"empty bucket", client, ""},
		{"blank bucket", client, " \t "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := New(tc.client, tc.bucket)
			if store != nil || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("store=%v error=%v", store, err)
			}
		})
	}
}
