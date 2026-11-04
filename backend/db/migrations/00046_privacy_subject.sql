-- +goose Up
-- Pedido do titular, executado pelo admin. O expurgo automático continua
-- recusado pela restrição de 00033. Esta coluna só marca o contato já
-- anonimizado para a repetição não apagar ciclo nem protocolo de novo.
-- media_items precisa de DELETE para soltar o anexo antes do registro
-- em deletion_records, que o ensaio de restore reaplica.

ALTER TABLE contacts
    ADD COLUMN privacy_erased_at timestamptz;

GRANT DELETE ON media_items TO chatisp_app;
GRANT UPDATE ON erp_actions TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'rollback descartaria a marca de exclusão do titular';
END $$;
-- +goose StatementEnd
