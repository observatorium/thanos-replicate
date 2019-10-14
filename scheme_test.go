package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"os"
	"path"
	"testing"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/oklog/ulid"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/prometheus/prometheus/tsdb/labels"
	"github.com/prometheus/tsdb/testutil"
	"github.com/thanos-io/thanos/pkg/block/metadata"
	"github.com/thanos-io/thanos/pkg/compact"
	"github.com/thanos-io/thanos/pkg/objstore"
	"github.com/thanos-io/thanos/pkg/objstore/inmem"
)

func testLogger(testName string) log.Logger {
	return log.With(
		level.NewFilter(log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)), level.AllowDebug()),
		"test", testName,
	)
}

func testULID(inc int64) ulid.ULID {
	timestamp := time.Unix(1000000+inc, 0)
	entropy := ulid.Monotonic(rand.New(rand.NewSource(timestamp.UnixNano())), 0)
	ulid := ulid.MustNew(ulid.Timestamp(timestamp), entropy)

	return ulid
}

func testMeta(ulid ulid.ULID) *metadata.Meta {
	return &metadata.Meta{
		Thanos: metadata.Thanos{
			Labels: map[string]string{
				"test-labelname": "test-labelvalue",
			},
			Downsample: metadata.ThanosDownsample{
				Resolution: int64(compact.ResolutionLevelRaw),
			},
		},
		BlockMeta: tsdb.BlockMeta{
			ULID: ulid,
			Compaction: tsdb.BlockMetaCompaction{
				Level: 0,
			},
			Version: metadata.MetaVersion1,
		},
	}
}

func TestReplicationSchemeAll(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(ctx context.Context, t *testing.T, originBucket, targetBucket objstore.Bucket)
		assert  func(ctx context.Context, t *testing.T, originBucket, targetBucket *inmem.Bucket)
	}{
		{
			name:    "EmptyOrigin",
			prepare: func(ctx context.Context, t *testing.T, originBucket, targetBucket objstore.Bucket) {},
			assert:  func(ctx context.Context, t *testing.T, originBucket, targetBucket *inmem.Bucket) {},
		},
		{
			name: "NoMeta",
			prepare: func(ctx context.Context, t *testing.T, originBucket, targetBucket objstore.Bucket) {
				_ = originBucket.Upload(ctx, path.Join(testULID(0).String(), "chunks", "000001"), bytes.NewReader(nil))
			},
			assert: func(ctx context.Context, t *testing.T, originBucket, targetBucket *inmem.Bucket) {
				if len(targetBucket.Objects()) != 0 {
					t.Fatal("TargetBucket should have been empty but is not.")
				}
			},
		},
		{
			name: "PartialMeta",
			prepare: func(ctx context.Context, t *testing.T, originBucket, targetBucket objstore.Bucket) {
				_ = originBucket.Upload(ctx, path.Join(testULID(0).String(), "meta.json"), bytes.NewReader([]byte("{")))
			},
			assert: func(ctx context.Context, t *testing.T, originBucket, targetBucket *inmem.Bucket) {
				if len(targetBucket.Objects()) != 0 {
					t.Fatal("TargetBucket should have been empty but is not.")
				}
			},
		},
		{
			name: "FullBlock",
			prepare: func(ctx context.Context, t *testing.T, originBucket, targetBucket objstore.Bucket) {
				ulid := testULID(0)
				meta := testMeta(ulid)

				b, err := json.Marshal(meta)
				testutil.Ok(t, err)
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "meta.json"), bytes.NewReader(b))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "chunks", "000001"), bytes.NewReader(nil))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "index"), bytes.NewReader(nil))
			},
			assert: func(ctx context.Context, t *testing.T, originBucket, targetBucket *inmem.Bucket) {
				if len(targetBucket.Objects()) != 3 {
					t.Fatal("TargetBucket should have one block made up of three objects replicated.")
				}
			},
		},
		{
			name: "PreviousPartialUpload",
			prepare: func(ctx context.Context, t *testing.T, originBucket, targetBucket objstore.Bucket) {
				ulid := testULID(0)
				meta := testMeta(ulid)

				b, err := json.Marshal(meta)
				testutil.Ok(t, err)
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "meta.json"), bytes.NewReader(b))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "chunks", "000001"), bytes.NewReader(nil))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "index"), bytes.NewReader(nil))

				_ = targetBucket.Upload(ctx, path.Join(ulid.String(), "meta.json"), io.LimitReader(bytes.NewReader(b), int64(len(b)-10)))
				_ = targetBucket.Upload(ctx, path.Join(ulid.String(), "chunks", "000001"), bytes.NewReader(nil))
				_ = targetBucket.Upload(ctx, path.Join(ulid.String(), "index"), bytes.NewReader(nil))
			},
			assert: func(ctx context.Context, t *testing.T, originBucket, targetBucket *inmem.Bucket) {
				for k := range originBucket.Objects() {
					if !bytes.Equal(originBucket.Objects()[k], targetBucket.Objects()[k]) {
						t.Fatalf("Object %s not equal in origin and target bucket.", k)
					}
				}
			},
		},
		{
			name: "OnlyUploadsRaw",
			prepare: func(ctx context.Context, t *testing.T, originBucket, targetBucket objstore.Bucket) {
				ulid := testULID(0)
				meta := testMeta(ulid)

				b, err := json.Marshal(meta)
				testutil.Ok(t, err)
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "meta.json"), bytes.NewReader(b))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "chunks", "000001"), bytes.NewReader(nil))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "index"), bytes.NewReader(nil))

				ulid = testULID(1)
				meta = testMeta(ulid)
				meta.Thanos.Downsample.Resolution = int64(compact.ResolutionLevel5m)

				b, err = json.Marshal(meta)
				testutil.Ok(t, err)
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "meta.json"), bytes.NewReader(b))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "chunks", "000001"), bytes.NewReader(nil))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "index"), bytes.NewReader(nil))
			},
			assert: func(ctx context.Context, t *testing.T, originBucket, targetBucket *inmem.Bucket) {
				expected := 3
				got := len(targetBucket.Objects())
				if got != expected {
					t.Fatalf("TargetBucket should have one block made up of three objects replicated. Got %d but expected %d objects.", got, expected)
				}
			},
		},
		{
			name: "UploadMultipleCandidatesWhenPresent",
			prepare: func(ctx context.Context, t *testing.T, originBucket, targetBucket objstore.Bucket) {
				ulid := testULID(0)
				meta := testMeta(ulid)

				b, err := json.Marshal(meta)
				testutil.Ok(t, err)
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "meta.json"), bytes.NewReader(b))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "chunks", "000001"), bytes.NewReader(nil))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "index"), bytes.NewReader(nil))

				ulid = testULID(1)
				meta = testMeta(ulid)

				b, err = json.Marshal(meta)
				testutil.Ok(t, err)
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "meta.json"), bytes.NewReader(b))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "chunks", "000001"), bytes.NewReader(nil))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "index"), bytes.NewReader(nil))
			},
			assert: func(ctx context.Context, t *testing.T, originBucket, targetBucket *inmem.Bucket) {
				expected := 6
				got := len(targetBucket.Objects())
				if got != expected {
					t.Fatalf("TargetBucket should have two blocks made up of three objects replicated. Got %d but expected %d objects.", got, expected)
				}
			},
		},
		{
			name: "LabelSelector",
			prepare: func(ctx context.Context, t *testing.T, originBucket, targetBucket objstore.Bucket) {
				ulid := testULID(0)
				meta := testMeta(ulid)

				b, err := json.Marshal(meta)
				testutil.Ok(t, err)
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "meta.json"), bytes.NewReader(b))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "chunks", "000001"), bytes.NewReader(nil))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "index"), bytes.NewReader(nil))

				ulid = testULID(1)
				meta = testMeta(ulid)
				meta.Thanos.Labels["test-labelname"] = "non-selected-value"

				b, err = json.Marshal(meta)
				testutil.Ok(t, err)
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "meta.json"), bytes.NewReader(b))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "chunks", "000001"), bytes.NewReader(nil))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "index"), bytes.NewReader(nil))
			},
			assert: func(ctx context.Context, t *testing.T, originBucket, targetBucket *inmem.Bucket) {
				expected := 3
				got := len(targetBucket.Objects())
				if got != expected {
					t.Fatalf("TargetBucket should have one block made up of three objects replicated. Got %d but expected %d objects.", got, expected)
				}
			},
		},
		{
			name: "NonZeroCompaction",
			prepare: func(ctx context.Context, t *testing.T, originBucket, targetBucket objstore.Bucket) {
				ulid := testULID(0)
				meta := testMeta(ulid)
				meta.BlockMeta.Compaction.Level = 1

				b, err := json.Marshal(meta)
				testutil.Ok(t, err)
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "meta.json"), bytes.NewReader(b))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "chunks", "000001"), bytes.NewReader(nil))
				_ = originBucket.Upload(ctx, path.Join(ulid.String(), "index"), bytes.NewReader(nil))
			},
			assert: func(ctx context.Context, t *testing.T, originBucket, targetBucket *inmem.Bucket) {
				if len(targetBucket.Objects()) != 0 {
					t.Fatal("TargetBucket should have been empty but is not.")
				}
			},
		},
	}

	for _, c := range cases {
		ctx := context.Background()
		originBucket := inmem.NewBucket()
		targetBucket := inmem.NewBucket()

		c.prepare(ctx, t, originBucket, targetBucket)

		r := newReplicationScheme(
			testLogger(t.Name()+"/"+c.name),
			NewBlockFilter(labels.Selector{
				labels.NewEqualMatcher("test-labelname", "test-labelvalue"),
			}).Filter,
			originBucket,
			targetBucket,
		)

		err := r.execute(ctx)
		testutil.Ok(t, err)

		c.assert(ctx, t, originBucket, targetBucket)
	}
}
