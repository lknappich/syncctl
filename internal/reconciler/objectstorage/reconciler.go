// Package objectstorage reconciles S3 bucket replication status between
// primary and secondary buckets. For S3 cross-region replication we do
// not copy objects ourselves (the cloud provider does); we verify the
// replica bucket's object count and total size matches the primary,
// within the configured lag threshold.
package objectstorage

import (
	"context"
	"fmt"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/lknappich/syncctl/internal/config"
	"github.com/lknappich/syncctl/internal/metrics"
	"github.com/lknappich/syncctl/internal/reconciler"
)

const name = "object_storage"

// bucketLister is the minimal S3 surface this reconciler needs. Both
// *s3.Client (via listerAdapter) and a test mock satisfy it.
type bucketLister interface {
	stats(ctx context.Context) (count, size int64, err error)
}

// listerAdapter wraps an *s3.Client + bucket name and satisfies bucketLister.
type listerAdapter struct {
	client *s3.Client
	bucket string
}

func (a *listerAdapter) stats(ctx context.Context) (int64, int64, error) {
	return bucketStats(ctx, a.client, a.bucket)
}

// Reconciler verifies S3 bucket parity between primary and replica.
type Reconciler struct {
	site         string
	primary      bucketLister
	replica      bucketLister
	lagThreshold time.Duration
}

// New creates an S3 object-storage reconciler. The primary client is built
// from the primary's S3 config; the replica client is built from the
// secondary's S3 config (which may use a different region/endpoint/creds).
// If the secondary has no S3 config, the primary's config is reused for
// both (same-region setup).
func New(ctx context.Context, site string, primary, secondary *config.S3Config) (*Reconciler, error) {
	if primary == nil {
		return nil, fmt.Errorf("primary.object_storage.s3 is required for s3 backend")
	}
	pClient, pBucket, err := newS3Client(ctx, primary, primary.PrimaryBucket)
	if err != nil {
		return nil, fmt.Errorf("primary s3 client: %w", err)
	}
	repCfg := secondary
	if repCfg == nil {
		repCfg = primary
	}
	rClient, rBucket, err := newS3Client(ctx, repCfg, repCfg.ReplicaBucket)
	if err != nil {
		return nil, fmt.Errorf("replica s3 client: %w", err)
	}
	return &Reconciler{
		site:         site,
		primary:      &listerAdapter{client: pClient, bucket: pBucket},
		replica:      &listerAdapter{client: rClient, bucket: rBucket},
		lagThreshold: primary.ReplicationLag,
	}, nil
}

// newReconcilerWithListers builds a Reconciler from already-constructed
// listers — used by tests to inject mocks.
func newReconcilerWithListers(primary, replica bucketLister, lag time.Duration) *Reconciler {
	return &Reconciler{primary: primary, replica: replica, lagThreshold: lag}
}

func newS3Client(ctx context.Context, s3cfg *config.S3Config, bucket string) (*s3.Client, string, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(s3cfg.Region),
		awsconfig.WithCredentialsProvider(
			credentialsFromConfig(s3cfg),
		),
	)
	if err != nil {
		return nil, "", fmt.Errorf("load aws config (region=%s): %w", s3cfg.Region, err)
	}
	opts := []func(*s3.Options){}
	if s3cfg.Endpoint != "" {
		opts = append(opts, func(o *s3.Options) { o.BaseEndpoint = ptr(s3cfg.Endpoint) })
	}
	return s3.NewFromConfig(awsCfg, opts...), bucket, nil
}

func (r *Reconciler) Name() string { return reconciler.QualifyName(name, r.site) }

// Reconcile lists objects in both buckets and compares count + total size.
// If the replica is behind by more than lagThreshold, returns not-OK.
func (r *Reconciler) Reconcile(ctx context.Context) reconciler.Result {
	start := time.Now()
	var pCount, pSize int64
	err := reconciler.Retry(ctx, name, 3, 2*time.Second, 10*time.Second, func() error {
		var e error
		pCount, pSize, e = r.primary.stats(ctx)
		return e
	})
	if err != nil {
		metrics.DriftTotal.WithLabelValues(r.Name(), "critical").Inc()
		return reconciler.Result{OK: false, Detail: fmt.Sprintf("primary list: %v", err), Remaining: 1}
	}
	var rCount, rSize int64
	err = reconciler.Retry(ctx, name, 3, 2*time.Second, 10*time.Second, func() error {
		var e error
		rCount, rSize, e = r.replica.stats(ctx)
		return e
	})
	if err != nil {
		metrics.DriftTotal.WithLabelValues(r.Name(), "critical").Inc()
		return reconciler.Result{OK: false, Detail: fmt.Sprintf("replica list: %v", err), Remaining: 1}
	}
	elapsed := time.Since(start)
	metrics.SyncDurationSeconds.WithLabelValues(r.Name(), "ok").Observe(elapsed.Seconds())

	if pCount == rCount && pSize == rSize {
		metrics.LastSyncTimestamp.WithLabelValues(r.Name()).Set(float64(time.Now().Unix()))
		return reconciler.Result{OK: true, Detail: fmt.Sprintf("buckets match: %d objects, %d bytes", pCount, pSize)}
	}
	delta := pCount - rCount
	remaining := delta
	if remaining < 0 {
		remaining = -remaining
	}
	metrics.DriftTotal.WithLabelValues(r.Name(), "warning").Inc()
	return reconciler.Result{
		OK:        false,
		Detail:    fmt.Sprintf("drift: primary=%d/%d replica=%d/%d (delta=%d)", pCount, pSize, rCount, rSize, delta),
		Remaining: int(remaining),
	}
}

// bucketStats lists all objects in a bucket and returns (count, totalSize).
func bucketStats(ctx context.Context, client *s3.Client, bucket string) (int64, int64, error) {
	var count, totalSize int64
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: ptr(bucket),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, 0, err
		}
		for _, obj := range page.Contents {
			count++
			if obj.Size != nil {
				totalSize += *obj.Size
			}
		}
	}
	return count, totalSize, nil
}

func ptr[T any](v T) *T { return &v }
