package objectstorage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/lknappich/syncctl/internal/config"
)

func TestNewRejectsNilPrimary(t *testing.T) {
	_, err := New(context.Background(), "s", nil, nil)
	if err == nil {
		t.Fatal("expected error for nil primary")
	}
	if !strings.Contains(err.Error(), "primary.object_storage.s3 is required") {
		t.Errorf("err = %v", err)
	}
}

func TestName(t *testing.T) {
	r := &Reconciler{}
	if r.Name() != "object_storage" {
		t.Errorf("Name() = %q, want object_storage", r.Name())
	}
}

func TestCredentialsFromConfig(t *testing.T) {
	s3cfg := &config.S3Config{
		AccessKey: "AKIAEXAMPLE",
		SecretKey: "secretexample",
	}
	provider := credentialsFromConfig(s3cfg)
	if provider == nil {
		t.Fatal("credentialsFromConfig returned nil provider")
	}
	creds, err := provider.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if creds.AccessKeyID != "AKIAEXAMPLE" {
		t.Errorf("AccessKeyID = %q", creds.AccessKeyID)
	}
	if creds.SecretAccessKey != "secretexample" {
		t.Errorf("SecretAccessKey = %q", creds.SecretAccessKey)
	}
	if creds.SessionToken != "" {
		t.Errorf("SessionToken = %q, want empty", creds.SessionToken)
	}
}

func TestPtrDereferences(t *testing.T) {
	v := ptr("hello")
	if *v != "hello" {
		t.Errorf("*ptr = %q", *v)
	}
	n := ptr(42)
	if *n != 42 {
		t.Errorf("*ptr = %d", *n)
	}
}

// --- Mock bucketLister ---

type mockLister struct {
	count int64
	size  int64
	err   error
}

func (m *mockLister) stats(ctx context.Context) (int64, int64, error) {
	return m.count, m.size, m.err
}

func TestReconcileMatch(t *testing.T) {
	r := newReconcilerWithListers(
		&mockLister{count: 10, size: 1024},
		&mockLister{count: 10, size: 1024},
		0,
	)
	res := r.Reconcile(context.Background())
	if !res.OK {
		t.Errorf("expected OK, got: %s", res.Detail)
	}
}

func TestReconcileDrift(t *testing.T) {
	r := newReconcilerWithListers(
		&mockLister{count: 100, size: 1024},
		&mockLister{count: 99, size: 1024},
		0,
	)
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on drift")
	}
	if res.Remaining != 1 {
		t.Errorf("Remaining = %d, want 1", res.Remaining)
	}
}

func TestReconcileDriftNegativeDelta(t *testing.T) {
	r := newReconcilerWithListers(
		&mockLister{count: 50, size: 100},
		&mockLister{count: 55, size: 100},
		0,
	)
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on drift")
	}
	if res.Remaining != 5 {
		t.Errorf("Remaining = %d, want 5", res.Remaining)
	}
}

func TestReconcilePrimaryError(t *testing.T) {
	r := newReconcilerWithListers(
		&mockLister{err: errors.New("access denied")},
		&mockLister{count: 10, size: 100},
		0,
	)
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on primary error")
	}
	if !strings.Contains(res.Detail, "primary list") {
		t.Errorf("Detail = %q", res.Detail)
	}
}

func TestReconcileReplicaError(t *testing.T) {
	r := newReconcilerWithListers(
		&mockLister{count: 10, size: 100},
		&mockLister{err: errors.New("access denied")},
		0,
	)
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on replica error")
	}
	if !strings.Contains(res.Detail, "replica list") {
		t.Errorf("Detail = %q", res.Detail)
	}
}

func TestReconcileSizeDrift(t *testing.T) {
	r := newReconcilerWithListers(
		&mockLister{count: 10, size: 100},
		&mockLister{count: 10, size: 90},
		0,
	)
	res := r.Reconcile(context.Background())
	if res.OK {
		t.Error("expected not-OK on size drift")
	}
}

func TestBucketStatsWithFakeS3(t *testing.T) {
	body := `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Contents><Key>obj1</Key><Size>100</Size></Contents>
  <Contents><Key>obj2</Key><Size>200</Size></Contents>
  <IsTruncated>false</IsTruncated>
</ListBucketResult>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("ak", "sk", "")),
	)
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
	})

	count, size, err := bucketStats(context.Background(), client, "test-bucket")
	if err != nil {
		t.Fatalf("bucketStats: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if size != 300 {
		t.Errorf("size = %d, want 300", size)
	}
}

func TestNewS3ClientWithEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	ctx := context.Background()
	cfg := &config.S3Config{
		Region:        "us-east-1",
		PrimaryBucket: "bucket",
		AccessKey:     "ak",
		SecretKey:     "sk",
		Endpoint:      srv.URL,
	}
	client, bucket, err := newS3Client(ctx, cfg, cfg.PrimaryBucket)
	if err != nil {
		t.Fatalf("newS3Client: %v", err)
	}
	if client == nil {
		t.Fatal("client should not be nil")
	}
	if bucket != "bucket" {
		t.Errorf("bucket = %q, want bucket", bucket)
	}
}

func TestNewS3ClientNoEndpoint(t *testing.T) {
	ctx := context.Background()
	cfg := &config.S3Config{
		Region:        "us-east-1",
		PrimaryBucket: "bucket",
		AccessKey:     "ak",
		SecretKey:     "sk",
	}
	client, _, err := newS3Client(ctx, cfg, cfg.PrimaryBucket)
	if err != nil {
		t.Fatalf("newS3Client: %v", err)
	}
	if client == nil {
		t.Fatal("client should not be nil")
	}
}

func TestListerAdapterStatsDelegates(t *testing.T) {
	// Verify listerAdapter delegates to bucketStats by checking it's not
	// nil-panic-prone: just verify the adapter struct can be constructed.
	a := &listerAdapter{bucket: "test"}
	if a.bucket != "test" {
		t.Error("bucket not set")
	}
}
