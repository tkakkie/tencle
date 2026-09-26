# ADR（設計判断の記録）

ADR（Architecture Decision Record）は、設計判断の背景、決定、検討した代わりの案、結果を残す文書である。結論だけでなく、なぜその判断をしたかを後から追えるようにする。

- [テンプレート](template.md)を使い、1つの設計判断を1つのADRに記録する
- ファイル名は `NNNN-英語のslug.md` とし、番号は再利用しない
- 状態は `提案`・`採用`・`置き換え（[ADR NNNN](NNNN-slug.md)）` のいずれか。決定を変えるときは新しいADRを書き、古いADRを「置き換え」にする。採用後の本文は書き換えない
- 不変条件は ADR より優先する。不変条件を変えるには、ADRに理由を書いたうえで invariants.md を変える

## 一覧

| 番号 | タイトル | 状態 |
|---|---|---|
| [0001](0001-record-decisions-in-adr.md) | 設計判断を ADR で記録する | 採用 |
| [0002](0002-rebuild-in-go.md) | Go で作り直し、AIのワークフローを軽くする | 採用 |
| [0003](0003-rls-implementation.md) | RLS の実現方式（TenantTx・例外関数・DBロール） | 採用 |
| [0004](0004-sessions-passwords-tokens.md) | セッションとパスワード、認証トークン | 採用 |
| [0005](0005-actor-storage.md) | Actor の保存方式 | 採用 |
| [0006](0006-user-creation-and-invitation.md) | User の作成手順、既存の User の招待の受諾、ユーザー単位の出来事の記録先 | 採用 |
| [0007](0007-job-errors-without-personal-data.md) | ジョブのエラーに個人情報を残さない | 採用 |
