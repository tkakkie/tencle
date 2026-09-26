-- スパイク：River のテーブル（rivermigrate が先に作る）への権限。River のテーブルは RLS の対象外で、個人情報を入れない（I-20）。

-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
    t text;
BEGIN
    FOR t IN SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename LIKE 'river\_%' LOOP
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO tencle_app', t);
    END LOOP;
END
$$;
-- +goose StatementEnd
-- シーケンスは River のジョブの ID のものだけ（River v0.47 の挿入が nextval を呼ぶ）。
GRANT USAGE ON SEQUENCE river_job_id_seq TO tencle_app;
-- テナントの作成と同じトランザクションで、パスワード設定のメールのジョブを入れるため。
-- River（v0.47）はジョブの挿入で RETURNING と ON CONFLICT (unique_key) DO UPDATE SET kind を使うので、SELECT と kind の UPDATE も要る。
GRANT SELECT, INSERT, UPDATE (kind) ON river_job TO tencle_tenant_creator;
GRANT USAGE ON SEQUENCE river_job_id_seq TO tencle_tenant_creator;

-- +goose Down
SELECT 1;
