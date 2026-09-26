# 用語集（Glossary）

すべての概念は、ひとつの**識別子**とひとつの**画面表示名**を持つ。

- **識別子**：コード、DBスキーマ、API、URL、ログで使う英語名
- **画面表示名**：UIに表示する日本語。画面に出ない概念は「—」

Issue・PR・ドキュメントでは、日本語の説明の中で識別子をそのまま使ってよい（例：「`Matter` のステータスを導出する」）。

これは辞書であり、仕様書ではない。概念ごとに識別子・表示名・短い意味を示すにとどめる。振る舞いや制約は [不変条件](../architecture/invariants.md)、[要件定義](../requirements.md)、ADRで定める。**ステータスの値と表示名は本書を正とする。**

**ここに合う言葉がないときは、同義語を作らず、同じPRでこのファイルを変更する。** AIが独自に訳語を作ることも禁止する。

同じ種類の識別子（型名どうし、同じ列挙の値どうしなど）の中で、同じ名前を2つの意味で使わない。種類が違うもの（型名の `Priest` と権限の段階の値 `priest`、案件種別と儀式種別のそれぞれの `funeral` など）は、本表で区別を明記したうえで許容する。

## テナント・アカウント・権限

| 識別子 | 表示名 | 意味 |
|---|---|---|
| `Tenant` | テナント | SaaSの契約単位であり、データの境界。寺院（`Temple`）とは別物 |
| `tenant_id` | — | テナント所有データが必ず持つ列。RLSとアプリ層のテナントスコープの基準 |
| テナント所有（tenant-owned） | — | `tenant_id` を持ち、テナント文脈とRLSで守られるテーブル。範囲は[不変条件](../architecture/invariants.md#テナント所有の範囲)を正とする |
| `app.tenant_id` / `app.user_id` | — | トランザクション単位で設定するテナント文脈・ユーザー文脈。RLSのポリシーが参照する |
| `Tenant.slug` | テナントコード | URLに含めるテナントの識別文字列。原則変更不可 |
| `User` | ユーザー | ログインする人のアカウント。システム全体で一意で、どのテナントにも属さない |
| `Membership` | 所属 | ユーザーの、あるテナントへの所属。権限の段階を持つ。業務データの「誰が」は `User` ではなくこちらを参照する |
| `Membership.active` | 有効 / 無効 | 退職時は削除せず無効化する |
| `Membership.access_level` | 権限 | `admin ⊃ office ⊃ priest`。上位は下位の権限をすべて含む |
| `admin` | 管理者 | 権限の段階。officeのすべてに加え、職員管理、マスタ管理、テナント設定、例外修正 |
| `office` | 事務 | 権限の段階。案件の受付・登録・参照・編集 |
| `priest`（権限の段階） | 僧侶 | 権限の段階。「自分の担当」だけを参照できる。僧侶マスタの `Priest` とは別物 |
| `my_assignments` | 自分の担当 | 僧侶に紐付いた職員が見られる、自分が確定割り当てされた予定の儀式。MVPでは最小限の一覧・詳細 |
| `admin_override` | 例外修正 | adminが理由を付けて、確定したステータスを戻す操作の総称（`restore_matter`、`reopen_ceremony`） |
| `Actor` | 操作者 | 書き込みを行う主体。`Membership` / `Integration` / `System` のいずれか |
| `System` | システム | ジョブ・CLIによる操作者。文書では「System Actor」とも書く |
| `Invitation` | 招待 | 管理者が職員をメールで招く仕組み。`priest` 段階では紐付けるPriestを指定する |

## DBとインフラ

| 識別子 | 表示名 | 意味 |
|---|---|---|
| DBロール | — | マイグレーション用・アプリ用・例外関数の読み取り用所有・例外関数の書き込み用所有・テナント作成用・保守用・バックアップ用。用途と権限は[不変条件](../architecture/invariants.md#dbロール)を正とする |
| 例外関数 | — | テナントを確定する前にテナント所有データ・`users`・認証トークンを読み書きしてよい専用のDB関数。一覧は[不変条件](../architecture/invariants.md#例外関数)を正とする |
| `lock_version` | — | 単独のエンティティの通常の編集で、上書きの競合を検出するための更新版番号 |
| 業務操作 | — | 複数の行やエンティティにまたがる判断を伴う変更の単位。一覧と操作名は[不変条件](../architecture/invariants.md#業務操作)を正とする |
| 消去台帳（erasure ledger） | — | Privacy Erasure の記録。IDと日時だけを持ち、DBの外にも保存する |
| 停止移行 | — | 直前のリリースと互換にできず、書き込みとジョブを止めて行うマイグレーション |
| RPO / RTO | — | 失ってよい更新の量 / 復旧までにかかってよい時間 |
| 許可リスト | — | 例外として認めるもの（モジュール、ジョブ、ログ属性など）を列挙したもの。ここにないものは認めない |

## テナント設定

| 識別子 | 表示名 | 意味 |
|---|---|---|
| `age_reckoning` | 数え方 | `kazoe`（数え年）/ `man`（満年齢）。テナントの既定値 |
| `age_label` | 年齢の表記 | `kyonen`（享年）/ `gyonen`（行年） |

## 顧客

| 識別子 | 表示名 | 意味 |
|---|---|---|
| `Family` | 家族 | CRM上の顧客単位 |
| `Family.primary_contact` | 主連絡先 | 家族の代表的な連絡先人物。同じ家族の連絡先人物に限る |
| `Contact` | 連絡先人物 | 家族に属する生存者。別居の家族も同じ家族に登録できる |
| `Contact.do_not_contact` | 案内不可 | 営業案内をしない |
| `Deceased` | 故人 | 家族に属する亡くなった方。1家族に複数 |
| `ContactDeceasedRelation` | 続柄 | 連絡先人物と故人の関係（例：長男、妻）。同じ家族の中に限る |
| `family_name` / `given_name` | 姓 / 名 | |
| `family_name_kana` / `given_name_kana` | 姓（かな）/ 名（かな） | |
| `date_of_birth` | 生年月日 | `date`。不明を許容 |
| `date_of_death` | 命日 | `date`。49日・年忌計算の基準 |
| `posthumous_name` | 戒名等 | 戒名・法名・法号などの総称。宗派によって呼び方が違うため中立的な名前にしている |
| `age_at_death` | 享年・行年 | 計算値。保存しない。手入力値があればそれを優先し、なければ生年月日と命日からテナント設定の数え方で計算する |
| `age_at_death_override` | 享年・行年（手入力） | 計算と異なる値にしたい場合や、生年月日が不明な場合に入力する |
| `age_at_death_override_reckoning` | 数え方（手入力） | 手入力値の数え方。`kazoe` / `man` |
| `Deceased.former_contact_id` | 元の連絡先人物 | 生存者として登録していた人が亡くなった場合に、同じ人物であることを対応付ける。同じ家族の中に限る |
| `Sect` | 宗派 | システム共通マスタ |

## 案件と儀式

| 識別子 | 表示名 | 意味 |
|---|---|---|
| `Matter` | 案件 | 紹介元から受けた仕事・受注の単位。葬儀と49日法要は別の案件 |
| 仮登録 | 仮登録 | Ceremonyが0件でキャンセルしていないMatter。ステータスは「受付中」 |
| `Matter.matter_number` | 案件番号 | テナント×年単位の自社採番（例：`2026-000001`） |
| `Matter.external_matter_number` | 外部案件番号 | 紹介元が付けた番号 |
| `Matter.received_at` | 受付日時 | 案件番号の年の基準（JST） |
| `Matter.acquisition_channel` | 獲得経路 | `referral`（紹介）/ `direct`（直接依頼）。直接依頼の場合、紹介元は空 |
| `Matter.referral_source` | 紹介元 | 獲得経路が紹介の場合に持つ（仮登録中は空を許す） |
| `Matter.referral_source_office` | 紹介元支店 | 任意 |
| `Matter.referral_source_contact` | 紹介元担当者 | 任意 |
| `Matter.receptionist` | 受付担当者 | `Membership` |
| `Matter.created_by` | 登録者 | `Actor` |
| `Matter.assignee` | 担当職員 | 現在の担当。`Membership` |
| `Matter.parent_matter` | 親案件 | 後続案件（例：葬儀→49日法要）の親。同じ家族の案件に限る |
| `Matter.sect_override` | 宗派上書き | 未設定なら主となる故人の宗派を使う |
| `Matter.cancelled_at` / `cancellation_reason` | キャンセル日時 / キャンセル理由 | |
| `MatterType` | 案件種別 | テナントごとのマスタ |
| `MatterDeceased` | 対象故人 | 案件と故人の中間テーブル |
| `MatterDeceased.primary` | 主となる故人 | 1案件に1人 |
| `MatterContact` | 案件関係者 | 案件内での連絡先人物の役割。同じ家族の連絡先人物に限る |
| `MatterContact.role` | 役割 | `chief_mourner` / `sponsor` / `liaison` |
| `chief_mourner` | 喪主 | `MatterContact.role` の値 |
| `sponsor` | 施主 | `MatterContact.role` の値。葬儀の費用負担者、法要の主催者 |
| `liaison` | 案件連絡先 | `MatterContact.role` の値。その案件でのやり取りの窓口 |
| `missing_info` | 不足情報 | 案件詳細に表示する、未入力・未割当などの注意事項（例：僧侶未割当）。ステータスではない |
| `Ceremony` | 儀式 | 案件の中で僧侶が実際に動く予定・実績（通夜、葬儀、法要など） |
| `CeremonyType` | 儀式種別 | テナントごとのマスタ |
| `system_code` | システムコード | 種別マスタの、業務ロジックが参照する固定コード。表示名はテナントが変えられるが、これは変えられない |
| `Ceremony.gathering_at` | 集合日時 | |
| `Ceremony.starts_at` | 開始日時 | |
| `Ceremony.ends_at` | 終了予定日時 | |
| `Ceremony.completed_at` | 完了日時 | |
| `Ceremony.cancelled_with_matter` | — | Matterのキャンセルと一緒にキャンセルされたか。Matterのキャンセルを取り消すときに予定へ戻す対象を決める |
| `Ceremony.location_snapshot` | 実施場所 | 寺院・会場・家族の住所・一時的な場所のいずれか。登録時点の場所を保存したもので、マスタが変わっても変わらない |
| `CeremonyAssignment` | 出仕割り当て | 儀式への僧侶の割り当て |
| `CeremonyAssignment.role` | 役割 | `officiant` / `assistant` |
| `CeremonyAssignment.temple_id` | 出仕時の所属寺院 | 割り当てを確定したときの僧侶の所属寺院。固定する |
| `Ceremony.completed_sect_id` | 執り行った宗派 | 儀式を完了したときの宗派。固定する |
| `officiant` | 導師 | `CeremonyAssignment.role` の値 |
| `assistant` | 脇導師 | `CeremonyAssignment.role` の値 |

### 案件種別の `system_code`

| `system_code` | 表示名（初期値） |
|---|---|
| `funeral` | 葬儀 |
| `memorial` | 法要 |

### 儀式種別の `system_code`

| `system_code` | 表示名（初期値） |
|---|---|
| `wake` | 通夜 |
| `funeral` | 葬儀・告別式 |
| `first_seventh_day` | 初七日 |
| `forty_ninth_day` | 四十九日法要 |
| `interment` | 納骨 |
| `anniversary_memorial` | 年忌法要 |
| `other_memorial` | その他法要 |

## 寺院・僧侶・紹介元・会場

| 識別子 | 表示名 | 意味 |
|---|---|---|
| `Temple` | 寺院 | テナントに属する寺院マスタ。自寺と提携寺院の両方を含む。テナント（`Tenant`）とは別物 |
| `Temple.kind` = `own` | 自寺 | テナントにちょうど1つ |
| `Temple.kind` = `partner` | 提携寺院 | 業務上の提携先。打診は寺院単位で行う |
| `Temple.service_areas` | 対応地域 | 寺院が出仕できる都道府県・市区町村 |
| `Priest` | 僧侶 | 自寺または提携寺院に所属する僧侶。権限の段階の `priest` とは別物 |
| `Priest.temple_kind` | — | 所属寺院の `kind` の写し。自寺の僧侶だけがログインできることをDBで保証するために持つ |
| `Priest.membership_id` | ログインアカウント | 自寺の僧侶がログインする場合のみ |
| `ReferralSource` | 紹介元 | 葬儀社など |
| `ReferralSourceOffice` | 紹介元支店 | |
| `ReferralSourceContact` | 紹介元担当者 | |
| `Venue` | 会場 | 繰り返し使う施設のマスタ（葬儀会館、火葬場など）。寺院は `Temple` で管理するため含めない。個人宅も含めない |
| `Prefecture` | 都道府県 | システム共通マスタ |
| `Municipality` | 市区町村 | システム共通マスタ |
| `PostalCode` | 郵便番号 | システム共通マスタ |

## 履歴・連携

| 識別子 | 表示名 | 意味 |
|---|---|---|
| `Activity` | 対応履歴 | 家族・案件への対応の記録。編集はできるが削除はできない |
| `Activity.type` | 種類 | `phone`（電話）/ `visit`（来訪・訪問）/ `email`（メール）/ `other`（その他）。初期値であり、実運用で見直す |
| `Activity.author` | 記録者 | `Actor` |
| `Activity.important` | 重要 | |
| `Activity.occurred_at` | 対応日時 | |
| `Activity.voided_at` / `void_reason` | 取消日時 / 取消理由 | 取り消した記録は通常の表示から外れ、admin だけが参照できる |
| audit log | 変更履歴 | 誰が・いつ・何を・どの値からどの値へ変えたかの記録。追記のみ |
| archive | アーカイブ | 通常検索から外すこと。復元できる |
| privacy erasure | 個人情報消去 | 個人情報を削除・識別不能化すること。復元できない |
| `Integration` | 外部連携 | APIキーを発行する外部システムの単位 |
| `Integration.permissions` | 権限 | その外部連携に許す操作・出力項目。それ以外は拒否する |
| `IntegrationKey` | APIキー | Integrationに属するAPIキー。複数持て、期限と失効を持つ。ハッシュで保存する |
| `Integration.enabled` | 有効 / 無効 | |
| `WebhookEndpoint` | Webhook送信先 | |
| `WebhookEvent` | Webhookイベント | 起きた事実の記録（Outbox）。ペイロードは通知だけで、個人情報を含めない |
| `WebhookDelivery` | Webhook配送 | WebhookEvent × WebhookEndpoint ごとの配送。試行回数・次回の時刻・結果を持つ |
| `schema_version` / リビジョン | — | Webhookのペイロードの版 / 対象の版 |
| `BulkOperation` | 一括処理 | 将来。依頼者・実行者・対象・項目ごとの結果・進捗を持つ一括処理の記録 |
| `Idempotency-Key` | 冪等キー | API書き込みの再送を二重処理しないためのキー（HTTPヘッダー名） |
| `IdempotencyRecord` | — | 冪等キーごとに保存する指紋（メソッド・ルートの型・対象・本文のハッシュ）、処理の状態、レスポンス。有効期限を持つ |
| `TempleRequest` | 打診 | 将来。儀式について提携寺院へ出仕を打診する。URLは `/temple-requests/{token}` |
| `TempleRequest.token_hash` | 打診リンク | ログイン不要で打診を見るためのトークンのハッシュ。平文は保存しない |

## 開発プロセス

| 識別子 | 表示名 | 意味 |
|---|---|---|
| 人間のアカウント | — | `tkakkie`。管理者。PRの承認に使う |
| botアカウント | — | AIがGitHubに書き込む（push・PR・コメント）ときに使うアカウント。PRを承認しない |
| 司令塔 | — | Issueの振り分けからPRの作成までを自動で進める Claude Code のセッション |
| `risk:high` | 高リスク | 人間が差分全体を読むPR。対象のパスは [workflow.md](../process/workflow.md#高リスク) を正とする |
| `type:*` | 種類 | Issue・PRの種類（`feature`・`bug`・`chore`・`docs`・`experiment`） |
| 認可コードのディレクトリ | — | `internal/authz/**`。認可を決めるコードはここにだけ置く |
| セキュリティ用リポジトリ | — | 非公開のリポジトリ `tencle-security`。実データを入れる前に用意し、セキュリティ系のチェック・監査・記録を扱う |

## ステータス

### `Ceremony.status`（保存する）

| 値 | 表示名 |
|---|---|
| `scheduled` | 予定 |
| `completed` | 完了 |
| `cancelled` | キャンセル |

### `Matter` のステータス（導出。保存しない）

| 値 | 表示名 |
|---|---|
| `intake` | 受付中 |
| `in_progress` | 進行中 |
| `completed` | 完了 |
| `cancelled` | キャンセル |

### `CeremonyAssignment.status`

| 値 | 表示名 |
|---|---|
| `tentative` | 仮 |
| `confirmed` | 確定 |
| `cancelled` | 取消 |

### `TempleRequest.status`（将来）

| 値 | 表示名 |
|---|---|
| `sent` | 打診中 |
| `applied` | 応募あり |
| `declined` | 辞退 |
| `accepted` | 採用 |
| `rejected` | 不採用 |
| `expired` | 期限切れ |
| `revoked` | 撤回 |

## 命名の経緯

| 対象 | 決定 |
|---|---|
| テナント = `Tenant`、寺院 = `Temple` | 契約テナントと寺院を分ける。打診は寺院単位で行うため寺院を独立したマスタとし、自寺と提携寺院を同じ `Temple` で表す。旧名 `PartnerTemple` は使わない |
| 案件 = `Matter` | 英語の case はテストケースなど他の意味と紛れやすいため、`Case` は使わない |
| 施主 = `sponsor` | 施主は葬儀の費用負担者に限らず、法要の主催者も指すため、支払いに限定した名前（`funeral_expense_payer` など）にはしない |
| 案件連絡先 = `liaison` | `Family.primary_contact` と同じ識別子になるのを避けるため |
| 権限 = `access_level` | ロールを複数持つ方式ではなく、段階を1つ持つ方式にした。僧侶として割り当てられるかは `Priest.membership_id` で別に表す |
| 割り当ての役割 | 当面は導師（`officiant`）と脇導師（`assistant`）のみ。役僧などは必要になった時点で追加する |
| 打診のURL = `/temple-requests/{token}` | 識別子 `TempleRequest` と一致させるため、`offers` などの別名は使わない |
| 打診の取消 = 撤回（`revoked`） | 出仕割り当ての「取消」と表示名が重ならないようにするため |
| プロジェクト名 = `tencle` | リポジトリ名・アプリ名・CLI（`tencle`）に使う |
| 享年・行年 | 数え方は宗派よりも地域・寺院・時代で異なるため、保存せず計算で求めることを基本とする。場合分けが必要になることを見込み、手入力で上書きできるようにする |
