# Runbook 检索评测集

`runbook_qrels.jsonl` 包含 60 条候选查询，RabbitMQ、Kubernetes、PostgreSQL 各 20 条。

人工检查每条查询及 `relevant_doc_ids` 后，将 `reviewed` 改为 `true`。`make rag-eval` 会拒绝任何未审核记录，避免用模型生成的标签作为正式验收依据。

运行评测需要配置 Embedding 与 Qdrant：

```bash
make rag-eval
```

输出包含文档级 Recall@3/5/8、MRR、NDCG@5、分组件 Recall@5 和未完全召回的查询 ID。
