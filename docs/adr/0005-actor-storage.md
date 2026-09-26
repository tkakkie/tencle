# ADR 0005：Actor の保存方式

- 状態：採用
- 日付：2026-09-26
- 関連：Issue #6、スパイクのブランチ `spike-6-walking-skeleton`、[要件定義 §5.5](../requirements.md#55-操作者actor)、I-3・I-4・I-6・I-37

## 背景

書き込みの「誰が」は User ではなく Actor（Membership・Integration・System）で表す（I-4）。Actor の保存方式（種類ごとの参照列を持つか等）は ADR で決めることにしていた（§5.5）。また、招待の受諾で監査に記録する Actor は M1 で決めることにしていた（I-6）。

## 決定

### 保存の形

Actor を記録する各テーブル（監査ログ、`Matter.created_by`、`Activity.author` など）は、次の列を持つ。

| 列 | 内容 |
|---|---|
| `actor_kind` | `membership`・`integration`・`system` |
| `actor_membership_id` | `actor_kind = 'membership'` のときだけ値を持つ。`(tenant_id, actor_membership_id)` の複合外部キーで Membership を参照する（I-3） |
| `actor_integration_id` | `actor_kind = 'integration'` のときだけ（M3 で足す）。同じく複合外部キー |
| `actor_system` | `actor_kind = 'system'` のときだけ。System Actor を作った処理の名前（例：`create-tenant`） |

- 「種類に合う列だけが値を持つ」ことを `CHECK` 制約で保証する（例：`CHECK ((actor_kind = 'membership') = (actor_membership_id IS NOT NULL))`）
- Go では、`internal/authz` の `Actor` 型（種類と ID）で受け渡し、ストアで上の列に分ける。System Actor を作る関数は1つにし、呼べるパッケージを限る（I-37）

### 招待の受諾の Actor

- 招待の受諾で書く監査（Membership の作成など）の Actor は、**受諾で作った Membership 自身**とする。新しく User を作った場合も、既に User を持つ人の場合も同じ
- 理由：受諾した人は、消費したトークンで特定できる。認証前のルート（ハンドラ）から System Actor を作ることは I-37 で禁じられており、招待した admin を Actor にすると、admin がしていない操作を admin の名前で記録することになる

### テナントの作成の Actor

- `tencle create-tenant`（CLI）の監査の Actor は System（`actor_system = 'create-tenant'`）とする。CLI は System Actor を作れるパッケージである（I-37）

## 検討した代わりの案

- **1つの `actor_id` 列と種類の列（多態の参照）**：外部キーを張れず、別テナントの Membership や存在しない ID を指せてしまう（I-3）
- **Actor の表を作り、各テーブルはその ID を参照する**：参照が1段増え、Actor の表自体のテナント分離と、Membership・Integration との対応を別に守る必要がある
- **招待の受諾の Actor を System にする**：I-37 に反する。招待した admin にする案は、上のとおり事実と違う記録になる

## 結果

- スパイクで、テナントの作成（System）・招待の作成（Membership）・受諾（作った Membership）の監査を記録し、受諾の監査の Actor が作った Membership を指すことをテストで確かめた
- Actor を持つテーブルごとに3〜4列と `CHECK` 制約が要る。sqlc のクエリと Go の型の変換は、`internal/authz` の `Actor` 型1か所にまとめる
- #12（監査ログの基盤）でこの形を使う
