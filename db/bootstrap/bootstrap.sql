-- スパイク：DB のロールと、データベースごとの初期設定。スーパーユーザーで実行する。
-- ロールはクラスタ単位なのでマイグレーションの外に置き、何度実行しても同じ結果になるようにする。
-- パスワードは開発用の架空の値（I-21）。本番ではデプロイの手順で別の値を与える。
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tencle_migrator') THEN
        CREATE ROLE tencle_migrator LOGIN BYPASSRLS PASSWORD 'dev-migrator';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tencle_app') THEN
        CREATE ROLE tencle_app LOGIN PASSWORD 'dev-app';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tencle_tenant_creator') THEN
        CREATE ROLE tencle_tenant_creator LOGIN BYPASSRLS PASSWORD 'dev-tenant-creator';
    END IF;
    -- 例外関数の所有者。ログインできない。
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tencle_fn_reader') THEN
        CREATE ROLE tencle_fn_reader NOLOGIN BYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tencle_fn_writer') THEN
        CREATE ROLE tencle_fn_writer NOLOGIN BYPASSRLS;
    END IF;
END
$$;

-- 例外関数の所有者を付け替えられるように、マイグレーション用ロールに SET だけを許す（権限は継承しない）。
GRANT tencle_fn_reader TO tencle_migrator WITH INHERIT FALSE, SET TRUE;
GRANT tencle_fn_writer TO tencle_migrator WITH INHERIT FALSE, SET TRUE;

-- ここからは接続しているデータベースの設定。マイグレーション用ロールがスキーマの所有者になり、テーブルを作る。
ALTER SCHEMA public OWNER TO tencle_migrator;
