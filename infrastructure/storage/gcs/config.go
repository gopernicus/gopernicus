package gcs

// Config holds GCS configuration.
type Config struct {
	BucketName            string
	ProjectID             string
	ServiceAccountKeyJSON string
}
