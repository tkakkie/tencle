# tencle

寺院向けのCRM。寺院が葬儀社などから受けた案件、家族・故人、葬儀・法要の予定、僧侶の出仕を管理する。

個人の趣味・学習プロジェクトであり、1人の開発者がAIを積極的に使って開発している。Go で書く。

## ドキュメント

| 文書 | 内容 |
|---|---|
| [要件定義](docs/requirements.md) | 要件と設計の背景 |
| [不変条件](docs/architecture/invariants.md) | 常に守るルールと、その担保の仕組み |
| [用語集](docs/domain/glossary.md) | 用語・識別子・ステータスの値 |
| [開発ワークフロー](docs/process/workflow.md) | AIと人間の役割、レビュー、CI |
| [AGENTS.md](AGENTS.md) | AIコーディングツール向けの実装規約 |

## 開発環境

Docker が必要。PostgreSQL を起動する。

```bash
cp .env.example .env
docker compose up -d --wait
```

止めるときは `docker compose down`。検査は `make check`（[AGENTS.md](AGENTS.md)）。

## 利用について

- このリポジトリはソースコードと設計を公開しているが、**オープンソースライセンスは付与していない**（All rights reserved）。コード・文書の利用、複製、改変、再配布は許諾していない
- システムを他の人が利用することは想定していない
- 外部からのコントリビューション（Pull Request）は受け付けていない

## 脆弱性の報告

脆弱性を見つけた場合は、公開のIssueではなく、GitHubの Security タブにある非公開の報告（private vulnerability reporting）で知らせてほしい。
