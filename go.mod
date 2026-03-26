module github.com/fourjs-mikefolcher/fglpkg

go 1.22

require (
	// AWS SDK v2 — used only by the registry server for Cloudflare R2 uploads.
	// R2 is S3-compatible so the standard S3 client works with a custom endpoint.
	github.com/aws/aws-sdk-go-v2                 v1.26.0
	github.com/aws/aws-sdk-go-v2/config          v1.27.0
	github.com/aws/aws-sdk-go-v2/credentials     v1.17.0
	github.com/aws/aws-sdk-go-v2/service/s3      v1.53.0
)
