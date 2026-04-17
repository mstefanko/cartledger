DROP INDEX IF EXISTS idx_shopping_list_items_assigned_store;
ALTER TABLE shopping_list_items DROP COLUMN assigned_store_id;
