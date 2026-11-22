CREATE UNIQUE INDEX movements_connection_external ON movements (connection_id, external_id) WHERE connection_id IS NOT NULL;
