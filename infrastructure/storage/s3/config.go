package s3

// Config holds S3-compatible storage configuration.
// Works with AWS S3, Digital Ocean Spaces, MinIO, and other S3-compatible services.
type Config struct {
	BucketName      string
	Region          string
	AccessKeyID     string
	SecretAccessKey string

	// CustomEndpoint is optional. If set, uses this endpoint instead of AWS defaults.
	// Examples:
	//   - Digital Ocean Spaces: "https://nyc3.digitaloceanspaces.com"
	//   - MinIO: "https://minio.example.com:9000"
	//   - Leave empty for AWS S3 (uses standard AWS endpoints)
	CustomEndpoint string
}
