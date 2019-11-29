package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"path"
	"sort"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/oklog/ulid"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/tsdb/labels"
	thanosblock "github.com/thanos-io/thanos/pkg/block"
	"github.com/thanos-io/thanos/pkg/block/metadata"
	"github.com/thanos-io/thanos/pkg/compact"
	"github.com/thanos-io/thanos/pkg/objstore"
	"github.com/thanos-io/thanos/pkg/runutil"
)

// BlockFilter is block filter that filters out compacted and unselected blocks.
type BlockFilter struct {
	logger          log.Logger
	labelSelector   labels.Selector
	resolutionLevel compact.ResolutionLevel
	compactionLevel int
}

// NewBlockFilter returns block filter.
func NewBlockFilter(
	logger log.Logger,
	labelSelector labels.Selector,
	resolutionLevel compact.ResolutionLevel,
	compactionLevel int,
) *BlockFilter {
	return &BlockFilter{
		labelSelector:   labelSelector,
		logger:          logger,
		resolutionLevel: resolutionLevel,
		compactionLevel: compactionLevel,
	}
}

// Filter return true if block is non-compacted and matches selector.
func (bf *BlockFilter) Filter(b *metadata.Meta) bool {
	blockLabels := labels.FromMap(b.Thanos.Labels)

	labelMatch := bf.labelSelector.Matches(blockLabels)
	if !labelMatch {
		selStr := "{"

		for i, m := range bf.labelSelector {
			if i != 0 {
				selStr += ","
			}

			selStr += m.String()
		}

		selStr += "}"

		level.Debug(bf.logger).Log("msg", "filtering block", "reason", "labels don't match", "block_labels", blockLabels.String(), "selector", selStr)

		return false
	}

	gotResolution := compact.ResolutionLevel(b.Thanos.Downsample.Resolution)
	expectedResolution := bf.resolutionLevel

	resolutionMatch := gotResolution == expectedResolution
	if !resolutionMatch {
		level.Debug(bf.logger).Log("msg", "filtering block", "reason", "resolutions don't match", "got_resolution", gotResolution, "expected_resolution", expectedResolution)
		return false
	}

	gotCompactionLevel := b.BlockMeta.Compaction.Level
	expectedCompactionLevel := bf.compactionLevel

	compactionMatch := gotCompactionLevel == expectedCompactionLevel
	if !compactionMatch {
		level.Debug(bf.logger).Log("msg", "filtering block", "reason", "compaction levels don't match", "got_compaction_level", gotCompactionLevel, "expected_compaction_level", expectedCompactionLevel)
		return false
	}

	return true
}

type blockFilterFunc func(b *metadata.Meta) bool

type replicationScheme struct {
	fromBkt objstore.BucketReader
	toBkt   objstore.Bucket

	blockFilter blockFilterFunc

	logger  log.Logger
	metrics *replicationMetrics
}

type replicationMetrics struct {
	originIterations  prometheus.Counter
	originMetaLoads   prometheus.Counter
	originPartialMeta prometheus.Counter

	blocksAlreadyReplicated prometheus.Counter
	blocksReplicated        prometheus.Counter
	objectsReplicated       prometheus.Counter
}

func newReplicationMetrics(reg prometheus.Registerer) *replicationMetrics {
	m := &replicationMetrics{
		originIterations: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "thanos_replicate_origin_iterations_total",
			Help: "Total number of objects iterated over in the origin bucket.",
		}),
		originMetaLoads: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "thanos_replicate_origin_meta_loads_total",
			Help: "Total number of meta.json reads in the origin bucket.",
		}),
		originPartialMeta: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "thanos_replicate_origin_partial_meta_reads_total",
			Help: "Total number of partial meta reads encountered.",
		}),
		blocksAlreadyReplicated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "thanos_replicate_blocks_already_replicated_total",
			Help: "Total number of blocks skipped due to already being replicated.",
		}),
		blocksReplicated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "thanos_replicate_blocks_replicated_total",
			Help: "Total number of blocks replicated.",
		}),
		objectsReplicated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "thanos_replicate_objects_replicated_total",
			Help: "Total number of objects replicated.",
		}),
	}

	if reg != nil {
		reg.MustRegister(m.originIterations)
		reg.MustRegister(m.originMetaLoads)
		reg.MustRegister(m.originPartialMeta)
		reg.MustRegister(m.blocksAlreadyReplicated)
		reg.MustRegister(m.blocksReplicated)
		reg.MustRegister(m.objectsReplicated)
	}

	return m
}

func newReplicationScheme(logger log.Logger, metrics *replicationMetrics, blockFilter blockFilterFunc, from objstore.BucketReader, to objstore.Bucket) *replicationScheme {
	if logger == nil {
		logger = log.NewNopLogger()
	}

	return &replicationScheme{
		logger:      logger,
		blockFilter: blockFilter,
		fromBkt:     from,
		toBkt:       to,
		metrics:     metrics,
	}
}

func (rs *replicationScheme) execute(ctx context.Context) error {
	availableBlocks := []*metadata.Meta{}

	level.Debug(rs.logger).Log("msg", "scanning blocks available blocks for replication")

	if err := rs.fromBkt.Iter(ctx, "", func(name string) error {
		rs.metrics.originIterations.Inc()

		id, ok := thanosblock.IsBlockDir(name)
		if !ok {
			return nil
		}

		rs.metrics.originMetaLoads.Inc()
		meta, metaNonExistentOrPartial, err := loadMeta(ctx, rs.fromBkt, id)
		if metaNonExistentOrPartial {
			// meta.json is the last file uploaded by a Thanos shipper,
			// therefore a block may be partially present, but no meta.json
			// file yet. If this is the case we skip that block for now.
			rs.metrics.originPartialMeta.Inc()
			level.Info(rs.logger).Log("msg", "block meta not uploaded yet. Skipping.", "block_uuid", id.String())
			return nil
		}
		if err != nil {
			return fmt.Errorf("load meta for block %v from origin bucket: %w", id.String(), err)
		}

		if len(meta.Thanos.Labels) == 0 {
			// TODO(bwplotka): Allow injecting custom labels as shipper does.
			level.Info(rs.logger).Log("msg", "block meta without Thanos external labels set. This is not allowed. Skipping.", "block_uuid", id.String())
			return nil
		}

		level.Debug(rs.logger).Log("msg", "adding block to available blocks", "block_uuid", id.String())

		availableBlocks = append(availableBlocks, meta)

		return nil
	}); err != nil {
		return fmt.Errorf("iterate over origin bucket: %w", err)
	}

	candidateBlocks := []*metadata.Meta{}

	for _, b := range availableBlocks {
		if rs.blockFilter(b) {
			level.Debug(rs.logger).Log("msg", "adding block to candidate blocks", "block_uuid", b.BlockMeta.ULID.String())
			candidateBlocks = append(candidateBlocks, b)
		}
	}

	// In order to prevent races in compactions by the target environment, we
	// need to replicate oldest start timestamp first.
	sort.Slice(candidateBlocks, func(i, j int) bool {
		return candidateBlocks[i].BlockMeta.MinTime < candidateBlocks[j].BlockMeta.MinTime
	})

	for _, b := range candidateBlocks {
		if err := rs.ensureBlockIsReplicated(ctx, b.BlockMeta.ULID); err != nil {
			return fmt.Errorf("ensure block %v is replicated: %w", b.BlockMeta.ULID.String(), err)
		}
	}

	return nil
}

// ensureBlockIsReplicated ensures that a block present in the origin bucket is
// present in the target bucket.
func (rs *replicationScheme) ensureBlockIsReplicated(ctx context.Context, id ulid.ULID) error {
	blockID := id.String()
	chunksDir := path.Join(blockID, thanosblock.ChunksDirname)
	indexFile := path.Join(blockID, thanosblock.IndexFilename)
	metaFile := path.Join(blockID, thanosblock.MetaFilename)

	level.Debug(rs.logger).Log("msg", "ensuring block is replicated", "block_uuid", blockID)

	originMetaFile, err := rs.fromBkt.Get(ctx, metaFile)
	if err != nil {
		return fmt.Errorf("get meta file from origin bucket: %w", err)
	}

	defer runutil.CloseWithLogOnErr(rs.logger, originMetaFile, "close original meta file")

	targetMetaFile, err := rs.toBkt.Get(ctx, metaFile)
	if targetMetaFile != nil {
		defer runutil.CloseWithLogOnErr(rs.logger, targetMetaFile, "close target meta file")
	}

	if err != nil && !rs.toBkt.IsObjNotFoundErr(err) && err != io.EOF {
		return fmt.Errorf("get meta file from target bucket: %w", err)
	}

	originMetaFileContent, err := ioutil.ReadAll(originMetaFile)
	if err != nil {
		return fmt.Errorf("read origin meta file: %w", err)
	}

	if targetMetaFile != nil && !rs.toBkt.IsObjNotFoundErr(err) {
		targetMetaFileContent, err := ioutil.ReadAll(targetMetaFile)
		if err != nil {
			return fmt.Errorf("read target meta file: %w", err)
		}

		if bytes.Equal(originMetaFileContent, targetMetaFileContent) {
			// If the origin meta file content and target meta file content is
			// equal, we know we have already successfully replicated
			// previously.
			level.Debug(rs.logger).Log("msg", "skipping block as already replicated", "block_uuid", id.String())
			rs.metrics.blocksAlreadyReplicated.Inc()

			return nil
		}
	}

	if err := rs.fromBkt.Iter(ctx, chunksDir, func(objectName string) error {
		err := rs.ensureObjectReplicated(ctx, objectName)
		if err != nil {
			return fmt.Errorf("replicate object %v: %w", objectName, err)
		}

		return nil
	}); err != nil {
		return err
	}

	if err := rs.ensureObjectReplicated(ctx, indexFile); err != nil {
		return fmt.Errorf("replicate index file: %w", err)
	}

	level.Debug(rs.logger).Log("msg", "replicating meta file", "object", metaFile)

	if err := rs.toBkt.Upload(ctx, metaFile, bytes.NewReader(originMetaFileContent)); err != nil {
		return fmt.Errorf("upload meta file: %w", err)
	}

	rs.metrics.blocksReplicated.Inc()

	return nil
}

// ensureBlockIsReplicated ensures that an object present in the origin bucket
// is present in the target bucket.
func (rs *replicationScheme) ensureObjectReplicated(ctx context.Context, objectName string) error {
	level.Debug(rs.logger).Log("msg", "ensuring object is replicated", "object", objectName)

	exists, err := rs.toBkt.Exists(ctx, objectName)
	if err != nil {
		return fmt.Errorf("check if %v exists in target bucket: %w", objectName, err)
	}

	// skip if already exists
	if exists {
		level.Debug(rs.logger).Log("msg", "skipping object as already replicated", "object", objectName)
		return nil
	}

	level.Debug(rs.logger).Log("msg", "object not present in target bucket, replicating", "object", objectName)

	r, err := rs.fromBkt.Get(ctx, objectName)
	if err != nil {
		return fmt.Errorf("get %v from origin bucket: %w", objectName, err)
	}

	defer r.Close()

	if err = rs.toBkt.Upload(ctx, objectName, r); err != nil {
		return fmt.Errorf("upload %v to target bucket: %w", objectName, err)
	}

	level.Info(rs.logger).Log("msg", "object replicated", "object", objectName)
	rs.metrics.objectsReplicated.Inc()

	return nil
}

// loadMeta loads the meta.json from the origin bucket and returns the meta
// struct as well as if failed, whether the failure was due to the meta.json
// not being present or partial. The distinction is important, as if missing or
// partial, this is just a temporary failure, as the block is still being
// uploaded to the origin bucket.
func loadMeta(ctx context.Context, bucket objstore.BucketReader, id ulid.ULID) (*metadata.Meta, bool, error) {
	src := path.Join(id.String(), thanosblock.MetaFilename)

	r, err := bucket.Get(ctx, src)
	if bucket.IsObjNotFoundErr(err) {
		return nil, true, fmt.Errorf("get meta file: %w", err)
	}

	if err != nil {
		return nil, false, fmt.Errorf("get meta file: %w", err)
	}

	defer r.Close()

	metaContent, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, false, fmt.Errorf("read meta file: %w", err)
	}

	var m metadata.Meta
	if err := json.Unmarshal(metaContent, &m); err != nil {
		return nil, true, fmt.Errorf("unmarshal meta: %w", err)
	}

	if m.Version != metadata.MetaVersion1 {
		return nil, false, errors.Errorf("unexpected meta file version %d", m.Version)
	}

	return &m, false, nil
}
