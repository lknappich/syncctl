package objectstorage

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/lknappich/syncctl/internal/config"
)

// credentialsFromConfig builds an aws.CredentialsProvider from the
// static access/secret keys in S3Config. These keys come from the
// environment via the config loader's ${ENV} expansion.
func credentialsFromConfig(s3cfg *config.S3Config) aws.CredentialsProvider {
	return credentials.NewStaticCredentialsProvider(
		s3cfg.AccessKey, s3cfg.SecretKey, "",
	)
}
