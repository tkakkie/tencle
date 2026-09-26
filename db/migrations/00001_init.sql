-- スパイク：テナント分離と認証の土台。マイグレーション用ロール（tencle_migrator）で実行する。

-- +goose Up
-- +goose StatementBegin

-- テナント文脈とユーザー文脈。SET LOCAL（set_config(..., true)）でトランザクション単位にだけ設定される（I-2）。
-- 未設定なら NULL を返し、RLS の比較がすべて偽になる。
CREATE FUNCTION app_tenant_id() RETURNS uuid
LANGUAGE sql STABLE
AS $$ SELECT nullif(current_setting('app.tenant_id', true), '')::uuid $$;

CREATE FUNCTION app_user_id() RETURNS uuid
LANGUAGE sql STABLE
AS $$ SELECT nullif(current_setting('app.user_id', true), '')::uuid $$;

CREATE TABLE tenants (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       text NOT NULL UNIQUE,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- 正規化したメールアドレス。正規化した値だけを保存するので、一意制約もその値に掛かる。
    email             text NOT NULL UNIQUE CHECK (email = lower(btrim(email))),
    -- パスワードを設定する前も、使えない本物の argon2id のハッシュを入れる（ログインの所要時間で有無が分からないように）。
    hashed_password   text NOT NULL,
    password_set_at   timestamptz,
    -- 認証の世代。パスワードの更新で1つ上げる。セッションは発行時の世代と一致するときだけ有効（ログインと更新の競合で古いセッションが残らないように）。
    credential_version integer NOT NULL DEFAULT 0,
    email_verified_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE memberships (
    tenant_id    uuid NOT NULL REFERENCES tenants (id),
    id           uuid NOT NULL DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users (id),
    access_level text NOT NULL CHECK (access_level IN ('admin', 'office', 'priest')),
    active       boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, user_id)
);

CREATE TABLE temples (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    id        uuid NOT NULL DEFAULT gen_random_uuid(),
    kind      text NOT NULL CHECK (kind IN ('own', 'partner')),
    name      text NOT NULL,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id)
);
-- 自寺はテナントにちょうど1つ（I-27）。
CREATE UNIQUE INDEX temples_one_own_per_tenant ON temples (tenant_id) WHERE kind = 'own';

CREATE TABLE invitations (
    tenant_id    uuid NOT NULL REFERENCES tenants (id),
    id           uuid NOT NULL DEFAULT gen_random_uuid(),
    email        text NOT NULL CHECK (email = lower(btrim(email))),
    access_level text NOT NULL CHECK (access_level IN ('admin', 'office', 'priest')),
    status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'revoked')),
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id)
);

-- 認証トークン（セッション・招待・パスワード再設定）。トークンそのものは保存せず、ハッシュだけを持つ（I-10）。
CREATE TABLE auth_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash  bytea NOT NULL UNIQUE,
    kind        text NOT NULL CHECK (kind IN ('session', 'invitation', 'password_reset')),
    -- 招待なら招待のID、パスワード再設定とセッションなら User のID。
    subject_id  uuid NOT NULL,
    -- セッションの発行時の User の認証の世代。セッション以外は NULL。
    credential_version integer,
    -- セッションとパスワード再設定の持ち主。招待では受諾まで User がないので NULL。
    user_id     uuid REFERENCES users (id),
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    revoked_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- 縦断の確認用のテナント所有テーブル。作成者は User ではなく Membership（Actor）で表す（I-4）。
CREATE TABLE notes (
    tenant_id                uuid NOT NULL REFERENCES tenants (id),
    id                       uuid NOT NULL DEFAULT gen_random_uuid(),
    body                     text NOT NULL,
    version                  integer NOT NULL DEFAULT 0,
    created_by_membership_id uuid NOT NULL,
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id),
    -- テナント所有テーブル間の参照は tenant_id を含む複合外部キー（I-3）。
    FOREIGN KEY (tenant_id, created_by_membership_id) REFERENCES memberships (tenant_id, id)
);

-- 監査ログ（テナント所有）。「誰が」は Actor の種類と、種類ごとの参照列で表す（I-4、ADR 0005 の候補）。
CREATE TABLE audit_logs (
    tenant_id           uuid NOT NULL REFERENCES tenants (id),
    id                  uuid NOT NULL DEFAULT gen_random_uuid(),
    actor_kind          text NOT NULL CHECK (actor_kind IN ('membership', 'system')),
    actor_membership_id uuid,
    -- System Actor を作った処理の名前（I-37。例：create-tenant）。
    actor_system        text,
    action              text NOT NULL,
    target_id           uuid NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    FOREIGN KEY (tenant_id, actor_membership_id) REFERENCES memberships (tenant_id, id),
    CHECK ((actor_kind = 'membership') = (actor_membership_id IS NOT NULL)),
    CHECK ((actor_kind = 'system') = (actor_system IS NOT NULL))
);

-- テナントに属さない、ユーザー単位の出来事（パスワードの変更・再設定など）。本人だけが見える。
CREATE TABLE user_events (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users (id),
    event      text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- RLS（I-2）。FORCE でテーブル所有者にも適用する。
ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenants_current ON tenants USING (id = app_tenant_id()) WITH CHECK (id = app_tenant_id());

ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
-- 自分と、現在のテナントの所属者だけが見える（I-34）。
CREATE POLICY users_visible ON users USING (
    id = app_user_id()
    OR EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = users.id AND m.tenant_id = app_tenant_id())
);

ALTER TABLE auth_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY auth_tokens_own ON auth_tokens USING (user_id = app_user_id());

ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY memberships_tenant ON memberships USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE temples ENABLE ROW LEVEL SECURITY;
ALTER TABLE temples FORCE ROW LEVEL SECURITY;
CREATE POLICY temples_tenant ON temples USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY invitations_tenant ON invitations USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE notes FORCE ROW LEVEL SECURITY;
CREATE POLICY notes_tenant ON notes USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE audit_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs FORCE ROW LEVEL SECURITY;
CREATE POLICY audit_logs_tenant ON audit_logs USING (tenant_id = app_tenant_id()) WITH CHECK (tenant_id = app_tenant_id());

ALTER TABLE user_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_events FORCE ROW LEVEL SECURITY;
CREATE POLICY user_events_own ON user_events USING (user_id = app_user_id()) WITH CHECK (user_id = app_user_id());

-- アプリ用ロールの権限（DBロールの表）。秘密の列（hashed_password・token_hash）は読めない。
GRANT USAGE ON SCHEMA public TO tencle_app, tencle_tenant_creator, tencle_fn_reader, tencle_fn_writer;
GRANT EXECUTE ON FUNCTION app_tenant_id(), app_user_id() TO tencle_app, tencle_fn_reader, tencle_fn_writer;
GRANT SELECT (id, slug, name, created_at) ON tenants TO tencle_app;
GRANT SELECT (id, email, email_verified_at, created_at) ON users TO tencle_app;
GRANT SELECT (id, kind, subject_id, user_id, expires_at, consumed_at, revoked_at, created_at) ON auth_tokens TO tencle_app;
GRANT SELECT, INSERT, UPDATE ON memberships, temples, invitations, notes TO tencle_app;
-- 集約の根として FOR UPDATE でロックするには、どれかの列の UPDATE 権限が要る（PostgreSQL の仕様）。テナント名の変更はテナント設定で行う。
GRANT UPDATE (name) ON tenants TO tencle_app;
GRANT SELECT, INSERT ON audit_logs, user_events TO tencle_app;

-- テナント作成用ロール（I-31）。slug の重複確認と既存 User の再利用のための SELECT と、作る表への INSERT だけ。
GRANT SELECT (id, slug) ON tenants TO tencle_tenant_creator;
GRANT SELECT (id, email) ON users TO tencle_tenant_creator;
GRANT INSERT ON tenants, temples, memberships, audit_logs TO tencle_tenant_creator;
GRANT INSERT (id, email, hashed_password) ON users TO tencle_tenant_creator;

-- 例外関数の所有ロールの権限。例外関数の表の「読む」「書く」列だけ。
GRANT SELECT (id, email, hashed_password, credential_version, password_set_at, email_verified_at) ON users TO tencle_fn_reader;
GRANT SELECT (id, slug, name) ON tenants TO tencle_fn_reader;
GRANT SELECT (id, tenant_id, user_id, access_level, active) ON memberships TO tencle_fn_reader;
GRANT SELECT (id, tenant_id, email, access_level, status, expires_at) ON invitations TO tencle_fn_reader;
GRANT SELECT (token_hash, kind, subject_id, user_id, expires_at, consumed_at, revoked_at, credential_version) ON auth_tokens TO tencle_fn_reader;
GRANT SELECT (id, email, credential_version) ON users TO tencle_fn_writer;
GRANT INSERT (email, hashed_password, password_set_at, email_verified_at) ON users TO tencle_fn_writer;
GRANT UPDATE (hashed_password, credential_version, password_set_at) ON users TO tencle_fn_writer;
GRANT SELECT (token_hash, kind, subject_id, user_id, expires_at, consumed_at, revoked_at) ON auth_tokens TO tencle_fn_writer;
GRANT INSERT (token_hash, kind, subject_id, user_id, expires_at, credential_version) ON auth_tokens TO tencle_fn_writer;
GRANT UPDATE (consumed_at, revoked_at) ON auth_tokens TO tencle_fn_writer;

-- ここから例外関数（不変条件の「例外関数」の表）。SECURITY DEFINER で所有ロールの権限で動き、
-- search_path を固定し、PUBLIC からの実行権限を外してアプリ用ロールだけに与える（I-1）。
-- pg_temp は省略すると一時スキーマが最初に検索され、一時テーブルで参照先を差し替えられるので、最後に明示する。

CREATE FUNCTION fn_login_lookup(p_email text, OUT user_id uuid, OUT hashed_password text, OUT credential_version integer)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp
AS $$ SELECT u.id, u.hashed_password, u.credential_version FROM users u WHERE u.email = p_email $$;

CREATE FUNCTION fn_tenant_by_slug(p_slug text) RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp
AS $$ SELECT t.id FROM tenants t WHERE t.slug = p_slug $$;

CREATE FUNCTION fn_user_by_email(p_email text) RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp
AS $$ SELECT u.id FROM users u WHERE u.email = p_email $$;

-- パスワード設定のメールのジョブが、処理の時点でまだ未設定かを確かめられるように、設定済みかどうかも返す（ハッシュは返さない）。
CREATE FUNCTION fn_user_email(p_user_id uuid, OUT email text, OUT password_set boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp
AS $$ SELECT u.email, u.password_set_at IS NOT NULL FROM users u WHERE u.id = p_user_id $$;

CREATE FUNCTION fn_invitation_lookup(p_invitation_id uuid,
    OUT tenant_id uuid, OUT status text, OUT expires_at timestamptz, OUT email text, OUT access_level text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp
AS $$ SELECT i.tenant_id, i.status, i.expires_at, i.email, i.access_level FROM invitations i WHERE i.id = p_invitation_id $$;

CREATE FUNCTION fn_my_memberships()
RETURNS TABLE (membership_id uuid, access_level text, active boolean, tenant_id uuid, slug text, name text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp
AS $$
    SELECT m.id, m.access_level, m.active, t.id, t.slug, t.name
    FROM memberships m JOIN tenants t ON t.id = m.tenant_id
    WHERE m.user_id = app_user_id()
$$;

CREATE FUNCTION fn_auth_token_check(p_token_hash bytea, p_kind text, OUT subject_id uuid, OUT user_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp
AS $$
    SELECT a.subject_id, a.user_id FROM auth_tokens a
    WHERE a.token_hash = p_token_hash AND a.kind = p_kind
      AND a.consumed_at IS NULL AND a.revoked_at IS NULL AND a.expires_at > now()
      AND (a.kind <> 'session'
           OR a.credential_version = (SELECT u.credential_version FROM users u WHERE u.id = a.user_id))
$$;

CREATE FUNCTION fn_auth_token_save(p_token_hash bytea, p_kind text, p_subject_id uuid, p_user_id uuid, p_expires_at timestamptz, p_credential_version integer)
RETURNS void
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp
AS $$
    -- パスワード再設定のトークンは、同じ User に対して常に1つだけ有効にする（前のものを失効させる）。
    UPDATE auth_tokens SET revoked_at = now()
    WHERE p_kind = 'password_reset' AND kind = 'password_reset' AND subject_id = p_subject_id
      AND consumed_at IS NULL AND revoked_at IS NULL;
    INSERT INTO auth_tokens (token_hash, kind, subject_id, user_id, expires_at, credential_version) VALUES (p_token_hash, p_kind, p_subject_id, p_user_id, p_expires_at, p_credential_version);
$$;

-- 1回だけ成立する（未消費かつ期限内の行だけを更新する）。
CREATE FUNCTION fn_auth_token_consume(p_token_hash bytea, p_kind text) RETURNS uuid
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp
AS $$
    UPDATE auth_tokens SET consumed_at = now()
    WHERE token_hash = p_token_hash AND kind = p_kind
      AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at > now()
    RETURNING subject_id
$$;

CREATE FUNCTION fn_revoke_user_sessions(p_user_id uuid) RETURNS void
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp
AS $$ UPDATE auth_tokens SET revoked_at = now() WHERE user_id = p_user_id AND kind = 'session' AND revoked_at IS NULL $$;

CREATE FUNCTION fn_update_password(p_user_id uuid, p_hashed_password text) RETURNS void
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp
AS $$
    UPDATE users SET hashed_password = p_hashed_password, credential_version = credential_version + 1, password_set_at = now() WHERE id = p_user_id;
    -- セッションと、未使用のパスワード再設定のトークンをすべて失効させる。
    UPDATE auth_tokens SET revoked_at = now()
    WHERE user_id = p_user_id AND kind IN ('session', 'password_reset') AND consumed_at IS NULL AND revoked_at IS NULL;
$$;

CREATE FUNCTION fn_create_user_by_invitation(p_email text, p_hashed_password text) RETURNS uuid
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp
AS $$ INSERT INTO users (email, hashed_password, password_set_at, email_verified_at) VALUES (p_email, p_hashed_password, now(), now()) RETURNING id $$;

-- 権限は所有者を付け替える前に設定する。付け替えた後は、マイグレーション用ロールは所有者でなくなり、
-- REVOKE・GRANT が警告だけで効かなくなるため（スパイクで見つけた）。
-- River の関数には触らないように、tencle の関数だけを PUBLIC から外す。
REVOKE EXECUTE ON FUNCTION
    app_tenant_id(), app_user_id(),
    fn_login_lookup(text), fn_tenant_by_slug(text), fn_user_by_email(text), fn_user_email(uuid),
    fn_invitation_lookup(uuid), fn_my_memberships(), fn_auth_token_check(bytea, text),
    fn_auth_token_save(bytea, text, uuid, uuid, timestamptz, integer), fn_auth_token_consume(bytea, text),
    fn_revoke_user_sessions(uuid), fn_create_user_by_invitation(text, text), fn_update_password(uuid, text)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app_tenant_id(), app_user_id() TO tencle_app, tencle_fn_reader, tencle_fn_writer;
GRANT EXECUTE ON FUNCTION
    fn_login_lookup(text), fn_tenant_by_slug(text), fn_user_by_email(text), fn_user_email(uuid),
    fn_invitation_lookup(uuid), fn_my_memberships(), fn_auth_token_check(bytea, text),
    fn_auth_token_save(bytea, text, uuid, uuid, timestamptz, integer), fn_auth_token_consume(bytea, text),
    fn_revoke_user_sessions(uuid), fn_create_user_by_invitation(text, text), fn_update_password(uuid, text)
TO tencle_app;

-- 所有者を付け替えるには、新しい所有者がスキーマの CREATE 権限を持つ必要がある（PostgreSQL の仕様）。付け替えの間だけ与える。
GRANT CREATE ON SCHEMA public TO tencle_fn_reader, tencle_fn_writer;
ALTER FUNCTION fn_login_lookup(text) OWNER TO tencle_fn_reader;
ALTER FUNCTION fn_tenant_by_slug(text) OWNER TO tencle_fn_reader;
ALTER FUNCTION fn_user_by_email(text) OWNER TO tencle_fn_reader;
ALTER FUNCTION fn_user_email(uuid) OWNER TO tencle_fn_reader;
ALTER FUNCTION fn_invitation_lookup(uuid) OWNER TO tencle_fn_reader;
ALTER FUNCTION fn_my_memberships() OWNER TO tencle_fn_reader;
ALTER FUNCTION fn_auth_token_check(bytea, text) OWNER TO tencle_fn_reader;
ALTER FUNCTION fn_auth_token_save(bytea, text, uuid, uuid, timestamptz, integer) OWNER TO tencle_fn_writer;
ALTER FUNCTION fn_auth_token_consume(bytea, text) OWNER TO tencle_fn_writer;
ALTER FUNCTION fn_revoke_user_sessions(uuid) OWNER TO tencle_fn_writer;
ALTER FUNCTION fn_create_user_by_invitation(text, text) OWNER TO tencle_fn_writer;
ALTER FUNCTION fn_update_password(uuid, text) OWNER TO tencle_fn_writer;
REVOKE CREATE ON SCHEMA public FROM tencle_fn_reader, tencle_fn_writer;

-- +goose StatementEnd

-- +goose Down
-- スパイクなので戻さない。
SELECT 1;
