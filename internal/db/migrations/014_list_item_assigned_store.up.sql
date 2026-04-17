ALTER TABLE shopping_list_items
  ADD COLUMN assigned_store_id TEXT REFERENCES stores(id) ON DELETE SET NULL;
CREATE INDEX idx_shopping_list_items_assigned_store
  ON shopping_list_items(assigned_store_id)
  WHERE assigned_store_id IS NOT NULL;
