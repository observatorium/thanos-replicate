package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/ioutil"
	"path"
	"sort"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/oklog/ulid"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/tsdb/labels"
	thanosblock "github.com/thanos-io/thanos/pkg/block"
	"github.com/thanos-io/thanos/pkg/block/metadata"
	"github.com/thanos-io/thanos/pkg/compact"
	"github.com/thanos-io/thanos/pkg/objstore"
)

type Block struct {
	ID   ulid.ULID
	Meta *metadata.Meta
}

type blocks []*Block

func (b blocks) Len() int {
	return len(b)
}

func (b blocks) Less(i, j int) bool {
	return b[i].Meta.MinTime < b[j].Meta.MinTime
}

func (b blocks) Swap(i, j int) {
	b[i], b[j] = b[j], b[i]
}

type BlockFilter struct {
	labelSelector labels.Selector
}

func NewBlockFilter(labelSelector labels.Selector) *BlockFilter {
	return &BlockFilter{
		labelSelector: labelSelector,
	}
}

func (bf *BlockFilter) Filter(b *Block) bool {
	blockLabels := labels.FromMap(b.Meta.Thanos.Labels)

	return bf.labelSelector.Matches(blockLabels) &&
		compact.ResolutionLevel(b.Meta.Thanos.Downsample.Resolution) == compact.ResolutionLevelRaw &&
		b.Meta.Compaction.Level == 0
}

type blockFilterFunc func(b *Block) bool

type replicationScheme struct {
	fromBkt objstore.BucketReader
	toBkt   objstore.Bucket

	blockFilter blockFilterFunc

	logger log.Logger
}

func newReplicationScheme(logger log.Logger, blockFilter blockFilterFunc, from objstore.BucketReader, to objstore.Bucket) *replicationScheme {
	if logger == nil {
		logger = log.NewNopLogger()
	}

	return &replicationScheme{
		logger:      logger,
		blockFilter: blockFilter,
		fromBkt:     from,
		toBkt:       to,
	}
}

func (rs *replicationScheme) execute(ctx context.Context) error {
	availableBlocks := blocks{}

	level.Debug(rs.logger).Log("msg", "scanning blocks available blocks for replication")

	err := rs.fromBkt.Iter(ctx, "", func(name string) error {
		// Strip trailing slash indicating a directory.
		id, err := ulid.Parse(name[:len(name)-1])
		if err != nil {
			return nil
		}

		meta, metaNonExistentOrPartial, err := loadMeta(ctx, rs.fromBkt, id)
		if metaNonExistentOrPartial {
			// meta.json is the last file uploaded by a Thanos shipper,
			// therefore a block may be partially present, but no meta.json
			// file yet. If this is the case we skip that block for now.
			level.Info(rs.logger).Log("msg", "block meta not uploaded yet. Skipping.", "block_uuid", id.String())
			return nil
		}
		if err != nil {
			return err
		}

		level.Debug(rs.logger).Log("msg", "adding block to available blocks", "block_uuid", id.String())

		availableBlocks = append(availableBlocks, &Block{
			ID:   id,
			Meta: meta,
		})

		return nil
	})

	if err != nil {
		return err
	}

	candidateBlocks := blocks{}

	for _, b := range availableBlocks {
		if rs.blockFilter(b) {
			level.Debug(rs.logger).Log("msg", "adding block to candidate blocks", "block_uuid", b.ID.String())
			candidateBlocks = append(candidateBlocks, b)
		}
	}

	// In order to prevent races in compactions by the target environment, we
	// need to replicate oldest start timestamp first.
	sort.Sort(sort.Reverse(candidateBlocks))

	for _, b := range candidateBlocks {
		if err := rs.ensureBlockIsReplicated(ctx, b.ID); err != nil {
			return err
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
		return err
	}

	defer originMetaFile.Close()

	targetMetaFile, err := rs.toBkt.Get(ctx, metaFile)
	if targetMetaFile != nil {
		defer targetMetaFile.Close()
	}

	if err != nil && !rs.toBkt.IsObjNotFoundErr(err) {
		return err
	}

	var originMetaFileContent, targetMetaFileContent []byte
	if targetMetaFile != nil && !rs.toBkt.IsObjNotFoundErr(err) {
		originMetaFileContent, err = ioutil.ReadAll(originMetaFile)
		if err != nil {
			return err
		}

		targetMetaFileContent, err = ioutil.ReadAll(targetMetaFile)
		if err != nil {
			return err
		}

		if bytes.Equal(originMetaFileContent, targetMetaFileContent) {
			// If the origin meta file content and target meta file content is
			// equal, we know we have already successfully replicated
			// previously.
			return nil
		}
	}

	if err := rs.fromBkt.Iter(ctx, chunksDir, func(objectName string) error {
		return rs.ensureObjectReplicated(ctx, objectName)
	}); err != nil {
		return err
	}

	if err := rs.ensureObjectReplicated(ctx, indexFile); err != nil {
		return err
	}

	level.Debug(rs.logger).Log("msg", "replicating meta file", "object", metaFile)

	if err := rs.toBkt.Upload(ctx, metaFile, bytes.NewReader(originMetaFileContent)); err != nil {
		return err
	}

	return nil
}

// ensureBlockIsReplicated ensures that an object present in the origin bucket
// is present in the target bucket.
func (rs *replicationScheme) ensureObjectReplicated(ctx context.Context, objectName string) error {
	level.Debug(rs.logger).Log("msg", "ensuring object is replicated", "object", objectName)

	exists, err := rs.toBkt.Exists(ctx, objectName)
	if err != nil {
		return err
	}

	// skip if already exists
	if exists {
		level.Debug(rs.logger).Log("msg", "skipping object as already replicated", "object", objectName)
		return nil
	}

	level.Debug(rs.logger).Log("msg", "object not present in target bucket, replicating", "object", objectName)

	r, err := rs.fromBkt.Get(ctx, objectName)
	if err != nil {
		return err
	}

	defer r.Close()

	err = rs.toBkt.Upload(ctx, objectName, r)
	if err != nil {
		return err
	}

	level.Debug(rs.logger).Log("msg", "object replicated", "object", objectName)

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
		return nil, true, err
	}

	if err != nil {
		return nil, false, err
	}

	defer r.Close()

	metaContent, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, false, err
	}

	var m metadata.Meta
	if err := json.Unmarshal(metaContent, &m); err != nil {
		return nil, true, err
	}

	if m.Version != metadata.MetaVersion1 {
		return nil, false, errors.Errorf("unexpected meta file version %d", m.Version)
	}

	return &m, false, nil
}
