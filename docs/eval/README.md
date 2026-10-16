# Runbook 检索评测集

`runbook_qrels.jsonl` 包含 60 条候选查询，RabbitMQ、Kubernetes、PostgreSQL 各 20 条。

人工检查每条查询及 `relevant_doc_ids` 后，将 `reviewed` 改为 `true`。`make rag-eval` 会拒绝任何未审核记录，避免用模型生成的标签作为正式验收依据。

运行评测需要配置 Embedding 与 Qdrant：

```bash
make rag-eval
```

输出包含文档级 Recall@3/5/8、MRR、NDCG@5、分组件 Recall@5 和未完全召回的查询 ID。

## BM25 与 reranker 验收

先在 `configs/config.local.yaml` 中关闭 reranker，保存 `BM25 + 向量 + RRF` 基线；再把 `rag.reranker` 配成下面的 Qwen 参数并启用，使用同一份 qrels 复测。`base_url` 填 DashScope API 的 `/api/v1` 前缀，适配器会追加文本重排路径：

```bash
make rag-eval > /tmp/rag-baseline.json
# 在 config.local.yaml 中设置 enabled=true、provider=qwen、base_url=https://maas.qianwenaiapi.com/api/v1、model=qwen3-rerank
make rag-eval > /tmp/rag-reranker.json
```

第一层以文档级 Recall@5 不回退为硬门槛，并比较 MRR、NDCG@5 与 `misses_at_5`；reranker 只能重排 `candidate_k` 个候选，无法找回候选集之外的文档。第二层使用同一批告警问题检查 Agent 是否引用正确 Runbook、诊断与证据是否一致，并同时记录端到端延迟。没有同时通过两层验收前，不默认启用 reranker。
