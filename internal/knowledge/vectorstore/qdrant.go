package vectorstore

import (
	"context"
	"crypto/md5"
	"fmt"

	qc "github.com/qdrant/go-client/qdrant"
)

// Qdrant 是基于 Qdrant 的向量存储实现。
type Qdrant struct {
	client     *qc.Client
	collection string
	dim        uint64
}

// NewQdrant 创建 Qdrant 向量存储（gRPC，默认端口 6334）。
func NewQdrant(host string, port int, collection string, dim uint64) (*Qdrant, error) {
	client, err := qc.NewClient(&qc.Config{Host: host, Port: port})
	if err != nil {
		return nil, err
	}
	return &Qdrant{client: client, collection: collection, dim: dim}, nil
}

// EnsureCollection 确保集合存在（不存在则创建，余弦距离）。
func (q *Qdrant) EnsureCollection(ctx context.Context) error {
	exists, err := q.client.CollectionExists(ctx, q.collection)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return q.client.CreateCollection(ctx, &qc.CreateCollection{
		CollectionName: q.collection,
		VectorsConfig:  qc.NewVectorsConfig(&qc.VectorParams{Size: q.dim, Distance: qc.Distance_Cosine}),
	})
}

// Upsert 写入向量点。原始 ID 存入 payload 的 chunk_id 字段（点 ID 为派生 UUID）。
func (q *Qdrant) Upsert(ctx context.Context, points []Point) error {
	pts := make([]*qc.PointStruct, 0, len(points))
	for _, p := range points {
		payload := map[string]*qc.Value{"chunk_id": qc.NewValueString(p.ID)}
		for k, v := range p.Payload {
			if s, ok := v.(string); ok {
				payload[k] = qc.NewValueString(s)
			}
		}
		pts = append(pts, &qc.PointStruct{
			Id:      qc.NewID(uuidFromString(p.ID)),
			Vectors: qc.NewVectorsDense(p.Vector),
			Payload: payload,
		})
	}
	_, err := q.client.Upsert(ctx, &qc.UpsertPoints{CollectionName: q.collection, Points: pts})
	return err
}

// Search 按余弦相似度检索 topK 个点。
func (q *Qdrant) Search(ctx context.Context, vector []float32, topK int) ([]SearchResult, error) {
	limit := uint64(topK)
	res, err := q.client.Query(ctx, &qc.QueryPoints{
		CollectionName: q.collection,
		Query:          qc.NewQueryDense(vector),
		Limit:          &limit,
		WithPayload:    qc.NewWithPayload(true),
	})
	if err != nil {
		return nil, err
	}

	out := make([]SearchResult, 0, len(res))
	for _, sp := range res {
		id := ""
		if sp.Payload != nil {
			if v, ok := sp.Payload["chunk_id"]; ok && v != nil {
				id = v.GetStringValue()
			}
		}
		out = append(out, SearchResult{ID: id, Score: sp.Score})
	}
	return out, nil
}

// uuidFromString 将任意字符串 ID 稳定映射为 UUID（Qdrant 点 ID 要求 UUID）。
func uuidFromString(s string) string {
	h := md5.Sum([]byte(s))
	h[6] = (h[6] & 0x0f) | 0x30
	h[8] = (h[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}
